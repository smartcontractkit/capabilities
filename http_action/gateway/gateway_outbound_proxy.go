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
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	gc "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const (
	internalError = "internal error"

	defaultGatewayConnectionInitialIntervalMs = 100    // 100 milliseconds
	defaultGatewayConnectionMaxElapsedTimeMs  = 30_000 // 30 seconds
	defaultGatewayConnectionMultiplier        = 2.0
)

var _ core.GatewayConnectorHandler = &gatewayOutboundProxy{}
var _ common.OutboundRequestClient = &gatewayOutboundProxy{}

type gatewayOutboundProxy struct {
	services.StateMachine
	gatewayConnector        core.GatewayConnector
	lggr                    logger.Logger
	incomingRateLimiter     *ratelimit.RateLimiter
	outgoingRateLimiter     *ratelimit.RateLimiter
	responses               *responses
	selectorOpts            []func(*gc.RoundRobinSelector)
	gatewayConnectionConfig common.GatewayConnectionConfig
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

func NewGatewayOutboundProxy(gatewayConnector core.GatewayConnector, config common.ServiceConfig, lggr logger.Logger, opts ...func(*gc.RoundRobinSelector)) (*gatewayOutboundProxy, error) {
	outgoingRateLimiter, err := ratelimit.NewRateLimiter(config.OutgoingRateLimiter)
	if err != nil {
		return nil, err
	}
	incomingRateLimiter, err := ratelimit.NewRateLimiter(config.IncomingRateLimiter)
	if err != nil {
		return nil, err
	}

	return &gatewayOutboundProxy{
		gatewayConnector:        gatewayConnector,
		responses:               newResponses(),
		outgoingRateLimiter:     outgoingRateLimiter,
		incomingRateLimiter:     incomingRateLimiter,
		lggr:                    lggr,
		selectorOpts:            opts,
		gatewayConnectionConfig: applyDefaults(config.GatewayConnectionConfig),
	}, nil
}

// SendRequest sends a request to gateway node and blocks until response is received
func (p *gatewayOutboundProxy) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request) (*http.Response, error) {
	requestID := common.GetRequestID(gc.MethodHTTPAction, metadata.WorkflowID, metadata.WorkflowExecutionID)
	lggr := logger.With(p.lggr, "requestID", requestID, "workflowID", metadata.WorkflowID, "workflowExecutionID", metadata.WorkflowExecutionID, "workflowOwner", metadata.WorkflowOwner)
	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.TimeoutMs)*time.Millisecond)
	defer cancel()

	workflowAllow, globalAllow := p.outgoingRateLimiter.AllowVerbose(metadata.WorkflowOwner)
	if !workflowAllow {
		return nil, errors.New(common.ErrorOutgoingRatelimitWorkflowOwner)
	}
	if !globalAllow {
		return nil, errors.New(common.ErrorOutgoingRatelimitGlobal)
	}

	gatewayReq := gc.OutboundHTTPRequest{
		WorkflowID: metadata.WorkflowID,
		URL:        input.Url,
		Method:     input.Method,
		Headers:    input.Headers,
		Body:       input.Body,
		// Casting is safe because input to this function is already validated
		TimeoutMs: uint32(input.TimeoutMs), //nolint:gosec // G115
		CacheSettings: gc.CacheSettings{
			ReadFromCache: input.CacheSettings.ReadFromCache,
			MaxAgeMs:      input.CacheSettings.MaxAgeMs,
		},
	}

	payload, err := json.Marshal(gatewayReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fetch request: %w", err)
	}

	responseCh, err := p.responses.new(requestID)
	if err != nil {
		return nil, fmt.Errorf("duplicate message received for ID: %s", requestID)
	}
	defer p.responses.cleanup(requestID)

	lggr.Debugw("sending request to gateway")

	rawRes := json.RawMessage(payload)
	gatewayResp := jsonrpc.Response[json.RawMessage]{
		Version: "2.0",
		ID:      requestID,
		Result:  &rawRes,
	}

	selectedGateway, err := p.awaitConnection(ctx, lggr, gatewayReq.Hash())
	if err != nil {
		return nil, errors.Join(errors.New("failed to await connection to gateway"), err)
	}

	if err := p.gatewayConnector.SendToGateway(ctx, selectedGateway, &gatewayResp); err != nil {
		return nil, errors.Join(errors.New("failed to send request to gateway"), err)
	}

	select {
	case resp := <-responseCh:
		lggr.Debugw("received response from gateway")
		if resp.ErrorMessage != "" {
			lggr.Errorw("error while receiving response from gateway", "errorMessage", resp.ErrorMessage)
			return nil, errors.New(internalError)
		}
		return &http.Response{
			StatusCode: uint32(resp.StatusCode), //nolint:gosec // G115
			Headers:    resp.Headers,
			Body:       resp.Body,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
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

	senderAllow, globalAllow := p.incomingRateLimiter.AllowVerbose(gatewayID)
	errorMsg := ""
	if !senderAllow {
		errorMsg = common.ErrorIncomingRatelimitSender
	} else if !globalAllow {
		errorMsg = common.ErrorIncomingRatelimitGlobal
	}

	if errorMsg != "" {
		l.Errorw("request rate-limited")
		msg = gc.OutboundHTTPResponse{
			ErrorMessage: errorMsg,
		}
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
