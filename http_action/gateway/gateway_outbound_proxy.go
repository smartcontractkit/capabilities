package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"stathat.com/c/consistent"

	"github.com/smartcontractkit/capabilities/http_action/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	gc "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const (
	internalError = "internal error"

	defaultGatewayConnectionInitialIntervalMs = 100 // 100 milliseconds
	defaultGatewayConnectionMaxElapsedTimeMs  = 5_000
	defaultGatewayConnectionMultiplier        = 2.0
)

var _ core.GatewayConnectorHandler = &gatewayOutboundProxy{}
var _ common.OutboundRequestClient = &gatewayOutboundProxy{}

type gatewayOutboundProxy struct {
	services.StateMachine
	gatewayConnector        core.GatewayConnector
	lggr                    logger.Logger
	responses               *responses
	gatewayConnectionConfig common.GatewayConnectionConfig
	metrics                 *common.Metrics
	validator               common.ResponseValidator
}

func applyDefaults(cfg common.GatewayConnectionConfig) common.GatewayConnectionConfig {
	if cfg.InitialIntervalMs == 0 {
		cfg.InitialIntervalMs = defaultGatewayConnectionInitialIntervalMs
	}
	if cfg.MaxElapsedTimeMs == 0 {
		cfg.MaxElapsedTimeMs = defaultGatewayConnectionMaxElapsedTimeMs
	}
	if cfg.Multiplier == 0 {
		cfg.Multiplier = defaultGatewayConnectionMultiplier
	}
	return cfg
}

func NewGatewayOutboundProxy(gatewayConnector core.GatewayConnector, config common.ServiceConfig, lggr logger.Logger, metrics *common.Metrics, validator common.ResponseValidator) (*gatewayOutboundProxy, error) {
	return &gatewayOutboundProxy{
		gatewayConnector:        gatewayConnector,
		responses:               newResponses(),
		lggr:                    lggr,
		gatewayConnectionConfig: applyDefaults(config.GatewayConnectionConfig),
		metrics:                 metrics,
		validator:               validator,
	}, nil
}

// SendRequest sends a request to gateway node and blocks until response is received
func (p *gatewayOutboundProxy) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request, startTime time.Time) (*http.Response, error) {
	ctx = metadata.ContextWithCRE(ctx)
	requestID := common.GetRequestID(gc.MethodHTTPAction, metadata.WorkflowID, metadata.WorkflowExecutionID)
	lggr := logger.With(p.lggr, "requestID", requestID, "workflowID", metadata.WorkflowID, "workflowExecutionID", metadata.WorkflowExecutionID, "workflowOwner", metadata.WorkflowOwner)
	ctx, cancel := context.WithTimeout(ctx, input.Timeout.AsDuration())
	defer cancel()

	gatewayReq := gc.OutboundHTTPRequest{
		WorkflowID:    metadata.WorkflowID,
		WorkflowOwner: metadata.WorkflowOwner,
		URL:           input.Url,
		Method:        input.Method,
		Headers:       input.Headers,
		Body:          input.Body,
		// Casting is safe because input to this function is already validated
		TimeoutMs: uint32(input.Timeout.AsDuration()), //nolint:gosec // G115
		CacheSettings: gc.CacheSettings{
			Store:    input.CacheSettings.Store,
			MaxAgeMs: int32(input.CacheSettings.MaxAge.AsDuration().Milliseconds()), //nolint:gosec // G115
		},
	}

	payload, err := json.Marshal(gatewayReq)
	if err != nil {
		p.metrics.IncrementExecutionError(ctx, common.ProxyModeGateway, lggr)
		lggr.Errorf("failed to marshal fetch request: %v", err)
		return nil, errors.New(internalError)
	}

	responseCh, err := p.responses.new(requestID)
	if err != nil {
		p.metrics.IncrementExecutionError(ctx, common.ProxyModeGateway, lggr)
		lggr.Errorf("duplicate message received for ID: %s", requestID)
		return nil, errors.New(internalError)
	}
	defer p.responses.cleanup(requestID)

	lggr.Debugw("sending request to gateway")

	rawRes := json.RawMessage(payload)
	gatewayResp := jsonrpc.Response[json.RawMessage]{
		Version: "2.0",
		ID:      requestID,
		Method:  gc.MethodHTTPAction,
		Result:  &rawRes,
	}

	p.metrics.IncrementRequestCount(ctx, lggr)
	selectedGateway, err := p.awaitConnection(ctx, lggr, gatewayReq.Hash())
	if err != nil {
		p.metrics.IncrementGatewaySendError(ctx, selectedGateway, lggr)
		lggr.Errorf("failed to await connection to gateway: %v", err)
		return nil, errors.New(internalError)
	}
	if err := p.gatewayConnector.SendToGateway(ctx, selectedGateway, &gatewayResp); err != nil {
		p.metrics.IncrementGatewaySendError(ctx, selectedGateway, lggr)
		lggr.Errorf("failed to send request to gateway: %v", err)
		return nil, errors.New(internalError)
	}

	select {
	case resp := <-responseCh:
		lggr.Debugw("received response from gateway")
		if resp.ErrorMessage != "" {
			lggr.Errorw("error while receiving response from gateway", "errorMessage", resp.ErrorMessage)
			if resp.IsExternalEndpointError {
				p.metrics.IncrementExternalEndpointError(ctx, common.ProxyModeGateway, lggr)
			} else {
				p.metrics.IncrementExecutionError(ctx, common.ProxyModeGateway, lggr)
			}
			return nil, errors.New(internalError)
		}
		response := &http.Response{
			StatusCode: uint32(resp.StatusCode), //nolint:gosec // G115
			Headers:    resp.Headers,
			Body:       resp.Body,
		}

		if err := p.validator.ValidateResponseSize(ctx, response.Body); err != nil {
			p.metrics.IncrementExternalEndpointError(ctx, common.ProxyModeGateway, lggr)
			return nil, err
		}

		totalLatency := time.Since(startTime).Milliseconds()
		p.metrics.RecordRequestLatency(ctx, totalLatency, resp.ExternalEndpointLatency.Milliseconds(), common.ProxyModeGateway, lggr)

		return response, nil
	case <-ctx.Done():
		p.metrics.IncrementExecutionError(ctx, common.ProxyModeGateway, lggr)
		lggr.Errorf("context done: %v", ctx.Err())
		return nil, errors.New(internalError)
	}
}

// awaitConnection attempts to establish a connection to a gateway using consistent hashing algorithm.
// Gateway node is selected based on the request hash. If the selected gateway is unavailable, it is removed
// from the consistent hash ring and the method retries to select another gateway.
// When all gateways are evicted from the hash ring, then it will retry to get the list of gateways and reinitialize the ring and retry after backoff.
// Note that consitent hash ring is reset every time a new request is made, so it will always use the latest list of gateways.
func (p *gatewayOutboundProxy) awaitConnection(ctx context.Context, lggr logger.Logger, requestHash string) (string, error) {
	gatewayIDs, err := p.gatewayConnector.GatewayIDs(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get gateway IDs: %w", err)
	}
	selector := setupRing(gatewayIDs)
	backoff := time.Duration(p.gatewayConnectionConfig.InitialIntervalMs) * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			if len(selector.Members()) == 0 {
				lggr.Warn("no available gateways found, retrying after backoff")
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(backoff):
					gatewayIDs, err := p.gatewayConnector.GatewayIDs(ctx)
					if err != nil {
						return "", fmt.Errorf("failed to get gateway IDs: %w", err)
					}
					selector = setupRing(gatewayIDs)
					backoff = p.nextBackoff(backoff)
					continue
				}
			}
			gateway, err := selector.Get(requestHash)
			if err != nil {
				return "", fmt.Errorf("failed to select gateway using consistent hashing: %w", err)
			}

			if err := p.attemptGatewayConnection(ctx, lggr, gateway, backoff); err != nil {
				lggr.Warnw("failed to await connection to gateway node, retrying", "err", err, "gateway", gateway)
				selector.Remove(gateway)
				continue
			}

			lggr.Debug("connected successfully")
			return gateway, nil
		}
	}
}

// attemptGatewayConnection waits to connect to a gateway with a new child context
func (p *gatewayOutboundProxy) attemptGatewayConnection(ctx context.Context, lggr logger.Logger, gateway string, timeout time.Duration) error {
	lggr.Debugw("awaiting connection", "timeout", timeout)

	// create a new child context to wait on gateway connection
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := p.gatewayConnector.AwaitConnection(ctxWithTimeout, gateway); err != nil {
		return fmt.Errorf("gateway connection failed: %w", err)
	}
	return nil
}

// HandleGatewayMessage processes incoming messages from the Gateway,
// which are in response to a HandleSingleNodeRequest call.
func (p *gatewayOutboundProxy) HandleGatewayMessage(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error {
	l := logger.With(p.lggr, "gatewayID", gatewayID, "method", req.Method, "requestID", req.ID)
	l.Debugw("handling incomming gateway message")
	if req.Params == nil {
		req.Params = &json.RawMessage{}
	}

	var msg gateway.OutboundHTTPResponse
	err := json.Unmarshal(*req.Params, &msg)
	if err != nil {
		l.Errorw("failed to unmarshal request params", "error", err)
		return nil
	}

	ch, ok := p.responses.get(req.ID)
	if !ok {
		l.Warnw("no response channel found; this may indicate that the node timed out the request")
		return nil
	}

	switch req.Method {
	case gc.MethodHTTPAction:
		select {
		case ch <- msg:
			return nil
		case <-ctx.Done():
			return nil
		}
	default:
		l.Errorw("unsupported method")
	}
	return nil
}

func (p *gatewayOutboundProxy) ID(ctx context.Context) (string, error) {
	return p.Name(), nil
}

func (p *gatewayOutboundProxy) Start(ctx context.Context) error {
	p.lggr.Debug("Starting GatewayOutboundProxy...")
	return p.StartOnce("GatewayOutboundProxy", func() error {
		return p.gatewayConnector.AddHandler(ctx, []string{gc.MethodHTTPAction}, p)
	})
}

func (p *gatewayOutboundProxy) Close() error {
	return p.StopOnce("GatewayOutboundProxy", func() error {
		return nil
	})
}

func (p *gatewayOutboundProxy) HealthReport() map[string]error {
	return map[string]error{p.Name(): p.Healthy()}
}

func (p *gatewayOutboundProxy) Name() string {
	return p.lggr.Name()
}

func newResponses() *responses {
	return &responses{
		chs: map[string]chan gc.OutboundHTTPResponse{},
	}
}

// nextBackoff calculates the next backoff duration using the configured multiplier and max elapsed time.
func (p *gatewayOutboundProxy) nextBackoff(backoff time.Duration) time.Duration {
	backoffMs := float64(backoff.Milliseconds())
	backoffMs = backoffMs * p.gatewayConnectionConfig.Multiplier
	backoffMs = math.Min(backoffMs, float64(p.gatewayConnectionConfig.MaxElapsedTimeMs))
	return time.Duration(backoffMs) * time.Millisecond
}

type responses struct {
	chs map[string]chan gc.OutboundHTTPResponse
	mu  sync.RWMutex
}

func (r *responses) new(id string) (chan gc.OutboundHTTPResponse, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.chs[id]
	if ok {
		return nil, fmt.Errorf("already have response for id: %s", id)
	}

	// Buffered so we don't wait if sending
	ch := make(chan gc.OutboundHTTPResponse, 1)
	r.chs[id] = ch
	return ch, nil
}

func (r *responses) cleanup(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.chs, id)
}

func (r *responses) get(id string) (chan gc.OutboundHTTPResponse, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.chs[id]
	return ch, ok
}

// setupRing initializes a consistent hash ring with the provided nodes.
func setupRing(gatewayIDs []string) *consistent.Consistent {
	c := consistent.New()
	for _, node := range gatewayIDs {
		c.Add(node)
	}
	return c
}
