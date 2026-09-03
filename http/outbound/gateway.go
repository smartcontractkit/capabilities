package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"stathat.com/c/consistent"

	"github.com/smartcontractkit/capabilities/http/common"

	"github.com/smartcontractkit/capabilities/http/protos"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gc "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const (
	defaultGatewayConnectionInitialIntervalMs = 100 // 100 milliseconds
	defaultGatewayConnectionMaxElapsedTimeMs  = 5_000
	defaultGatewayConnectionMultiplier        = 2.0
)

var _ common.Outbound = &gatewayOutboundProxy{}

// routingGatewayConnector is the subset of MultiGatewayConnector used for outbound routing.
type routingGatewayConnector interface {
	core.GatewayConnector
	GatewayIDsForDon(ctx context.Context, donID string) ([]string, error)
}

type gatewayOutboundProxy struct {
	services.StateMachine
	// gateway is the connection in both its roles: what a cached request is sent
	// over for the gateway to fetch, and what an uncached one is tunnelled through
	// for this node to fetch itself. See tunnel.go.
	gateway                 common.Gateway
	lggr                    logger.Logger
	gatewayConnectionConfig common.GatewayConnectionConfig
	metrics                 *common.Metrics

	// settings is where the gateway DON a workflow's requests go to is looked up.
	// It is per-workflow, so it is a lookup rather than a value.
	settings settings.Getter
}

// gatewayHeadersFromInput returns either Headers or MultiHeaders for the gateway request, never both.
// Caller must ensure input was validated (ValidatedRequest enforces at most one of Headers or MultiHeaders set).
func gatewayHeadersFromInput(input *protos.Request) (headers map[string]string, multiHeaders map[string][]string) {
	if len(input.MultiHeaders) > 0 {
		multiHeaders = make(map[string][]string, len(input.MultiHeaders))
		for k, v := range input.MultiHeaders {
			multiHeaders[k] = slices.Clone(v.GetValues())
		}
		return nil, multiHeaders
	}
	return input.Headers, nil //nolint:staticcheck // Headers deprecated but still used when MultiHeaders not set
}

// responseHeadersFromGateway converts a gateway OutboundHTTPResponse into Headers and MultiHeaders
// for the cap protos.Response. The gateway response must have exactly one of Headers or MultiHeaders set;
// the other is derived from it. Both fields are always set on the cap response (one populated, one derived).
func responseHeadersFromGateway(resp *gc.OutboundHTTPResponse) (headers map[string]string, multiHeaders map[string]*protos.HeaderValues) {
	if len(resp.MultiHeaders) > 0 {
		// Source is MultiHeaders: populate multiHeaders, derive headers (comma-joined for backward compatibility).
		multiHeaders = make(map[string]*protos.HeaderValues, len(resp.MultiHeaders))
		headers = make(map[string]string, len(resp.MultiHeaders))
		for k, v := range resp.MultiHeaders {
			// Sanitize invalid UTF-8: proto string fields must be valid UTF-8 to marshal over gRPC.
			key := common.SanitizeUTF8(k)
			sanitized := make([]string, len(v))
			for i, val := range v {
				sanitized[i] = common.SanitizeUTF8(val)
			}
			multiHeaders[key] = &protos.HeaderValues{Values: sanitized}
			if len(sanitized) > 0 {
				headers[key] = strings.Join(sanitized, ",")
			}
		}
		return headers, multiHeaders
	}
	// Source is Headers: populate headers, derive multiHeaders (single value per key).
	// Sanitize invalid UTF-8: proto string fields must be valid UTF-8 to marshal over gRPC.
	srcHeaders := resp.Headers //nolint:staticcheck // Headers deprecated but gateway may send it
	headers = make(map[string]string, len(srcHeaders))
	multiHeaders = make(map[string]*protos.HeaderValues, len(srcHeaders))
	for k, v := range srcHeaders {
		key := common.SanitizeUTF8(k)
		val := common.SanitizeUTF8(v)
		headers[key] = val
		multiHeaders[key] = &protos.HeaderValues{Values: []string{val}}
	}
	return headers, multiHeaders
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

// NewGatewayOutboundProxy returns the Outbound that sends requests to the DON's
// gateway - to be fetched there when the workflow wants them cached, or tunnelled
// through when it does not.
func NewGatewayOutboundProxy(gateway common.Gateway, config common.GatewayConnectionConfig, lggr logger.Logger, limitsFactory limits.Factory) (*gatewayOutboundProxy, error) {
	if gateway == nil {
		return nil, errors.New("a gateway outbound proxy needs a gateway connection: it is what its requests go out through")
	}

	metrics, err := common.NewMetrics()
	if err != nil {
		return nil, err
	}

	return &gatewayOutboundProxy{
		gateway:                 gateway,
		lggr:                    logger.Named(lggr, "Gateway"),
		gatewayConnectionConfig: applyDefaults(config),
		metrics:                 metrics,
		settings:                limitsFactory.Settings,
	}, nil
}

// SendRequest puts the request to the gateway and blocks until it is answered.
//
// Which way it goes is the workflow's own choice, made by what it asked of the
// cache. Cached, the gateway fetches once and every node of the DON is served the
// same answer, which is how they agree on what the internet said - and what the
// gateway sees is the request and the response. Uncached, each node was always
// going to get its own answer, so this node makes the request itself through a
// tunnel the gateway carries without reading.
func (p *gatewayOutboundProxy) SendRequest(ctx context.Context, request gc.OutboundHTTPRequest) (gc.OutboundHTTPResponse, error) {
	// A fresh ID per request: it is what the answer is matched back by, and
	// GetRequestID ends it with a UUID, so the workflow in it is for reading rather
	// than for telling two requests apart.
	requestID := common.GetRequestID(gc.MethodHTTPAction, request.WorkflowID)
	lggr := logger.With(p.lggr, "requestID", requestID, "workflowID", request.WorkflowID, "workflowOwner", request.WorkflowOwner)

	// What the workflow asked to wait, bounding both ways out of here: the gateway
	// fetching for us, and us fetching through it.
	timeout := time.Duration(request.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = common.DefaultRequestTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	donID, err := p.donID(ctx)
	if err != nil {
		p.metrics.IncrementExecutionError(ctx, lggr)
		return gc.OutboundHTTPResponse{}, fmt.Errorf("failed to resolve gateway proxy DON: %w", err)
	}

	selectedGateway, err := p.awaitConnection(ctx, lggr, donID, request.Hash())
	if err != nil {
		p.metrics.IncrementGatewaySendError(ctx, selectedGateway, donID, lggr)
		return gc.OutboundHTTPResponse{}, fmt.Errorf("failed to establish connection to gateway: %w", err)
	}

	if !wantsCache(request.CacheSettings) {
		return p.tunnelled(ctx, lggr, selectedGateway, request)
	}

	payload, err := json.Marshal(request)
	if err != nil {
		p.metrics.IncrementExecutionError(ctx, lggr)
		return gc.OutboundHTTPResponse{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	rawRes := json.RawMessage(payload)
	gatewayResp := jsonrpc.Response[json.RawMessage]{
		Version: "2.0",
		ID:      requestID,
		Method:  gc.MethodHTTPAction,
		Result:  &rawRes,
	}

	lggr.Debugw("sending request to gateway", "donID", donID, "selectedGateway", selectedGateway)

	started := time.Now()
	p.metrics.IncrementGatewaySendCount(ctx, selectedGateway, donID, lggr)

	// The answer comes back on this request: the gateway fetches while it is held
	// open, so there is nothing to correlate at either end, and a connection that
	// breaks is this request failing rather than an answer that never arrives.
	answer, err := p.gateway.Request(ctx, selectedGateway, &gatewayResp)
	if err != nil {
		// The wait ending is the workflow's own timeout, and is its own to be told
		// about: what the gateway is doing about the request may well outlive it, since
		// the fetch is on behalf of the whole DON.
		if ctx.Err() != nil {
			p.metrics.IncrementExecutionTimeout(ctx, lggr)
			elapsedMs := time.Since(started).Milliseconds()
			cause := context.Cause(ctx)
			lggr.Debugw(ErrMsgGatewayResponseWait, "elapsedMs", elapsedMs, "timeoutMs", timeout.Milliseconds(), "cause", cause)
			return gc.OutboundHTTPResponse{}, common.NewUserError(
				fmt.Errorf("%s (elapsedMs: %d, timeoutMs: %d): %w", ErrMsgGatewayResponseWait, elapsedMs, timeout.Milliseconds(), cause),
			)
		}

		p.metrics.IncrementGatewaySendError(ctx, selectedGateway, donID, lggr)
		return gc.OutboundHTTPResponse{}, fmt.Errorf("failed to send request to gateway: %w", err)
	}

	resp, err := outboundResponse(answer)
	if err != nil {
		p.metrics.IncrementExecutionError(ctx, lggr)
		return gc.OutboundHTTPResponse{}, err
	}

	lggr.Debugw("received response from gateway")
	if resp.ErrorMessage == "" {
		return resp, nil
	}

	lggr.Errorw("error while receiving response from gateway", "errorMessage", resp.ErrorMessage)
	switch {
	case resp.IsExternalEndpointError:
		p.metrics.IncrementExternalEndpointError(ctx, lggr)
		return resp, common.NewUserError(errors.New(resp.ErrorMessage))
	case resp.IsValidationError:
		p.metrics.IncrementInputValidationFailures(ctx, lggr)
		return resp, common.NewUserError(errors.New(resp.ErrorMessage))
	default:
		p.metrics.IncrementExecutionError(ctx, lggr)
		return resp, fmt.Errorf("gateway returned error: %s", resp.ErrorMessage)
	}
}

// outboundResponse reads what the gateway answered with, which is a JSON-RPC
// request because that is the shape it would have pushed at this node.
func outboundResponse(answer *jsonrpc.Request[json.RawMessage]) (gc.OutboundHTTPResponse, error) {
	if answer == nil || answer.Params == nil {
		return gc.OutboundHTTPResponse{}, errors.New("the gateway answered the request with no response in it")
	}
	if answer.Method != gc.MethodHTTPAction {
		return gc.OutboundHTTPResponse{}, fmt.Errorf("the gateway answered an outbound request with %q", answer.Method)
	}

	var response gc.OutboundHTTPResponse
	if err := json.Unmarshal(*answer.Params, &response); err != nil {
		return gc.OutboundHTTPResponse{}, fmt.Errorf("the gateway answered with something that is not an outbound HTTP response: %w", err)
	}
	return response, nil
}

// donID is the gateway DON this workflow's requests go to, which a workflow may
// be configured onto one of rather than taking the node's own.
func (p *gatewayOutboundProxy) donID(ctx context.Context) (string, error) {
	return cresettings.Default.PerWorkflow.HTTPAction.GatewayProxyDonID.GetOrDefault(ctx, p.settings)
}

// awaitConnection attempts to establish a connection to a gateway using consistent hashing algorithm.
// Gateway node is selected based on the request hash. If the selected gateway is unavailable, it is removed
// from the consistent hash ring and the method retries to select another gateway.
// When all gateways are evicted from the hash ring, then it will retry to get the list of gateways and reinitialize the ring and retry after backoff.
// Note that consitent hash ring is reset every time a new request is made, so it will always use the latest list of gateways.
func (p *gatewayOutboundProxy) awaitConnection(ctx context.Context, lggr logger.Logger, donID, requestHash string) (string, error) {
	gatewayIDs, err := p.gatewayIDsForDon(ctx, donID)
	if err != nil {
		return "", err
	}
	selector := setupRing(gatewayIDs)
	backoff := time.Duration(p.gatewayConnectionConfig.InitialIntervalMs) * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			if len(selector.Members()) == 0 {
				p.metrics.IncrementNoGatewaysAvailable(ctx, donID, lggr)
				lggr.Warn("no available gateways found, retrying after backoff")
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(backoff):
					gatewayIDs, err := p.gatewayIDsForDon(ctx, donID)
					if err != nil {
						return "", err
					}
					lggr.Debugw("setting up ring", "gatewayIDs", gatewayIDs, "donID", donID)
					selector = setupRing(gatewayIDs)
					backoff = p.nextBackoff(backoff)
					continue
				}
			}
			gateway, err := selector.Get(requestHash)
			if err != nil {
				return "", fmt.Errorf("failed to select gateway using consistent hashing: %w", err)
			}

			lggr = logger.With(lggr, "selectedGateway", gateway, "donID", donID)

			if err := p.attemptGatewayConnection(ctx, lggr, gateway, backoff); err != nil {
				lggr.Warnw("failed to await connection to gateway node, retrying", "err", err)
				selector.Remove(gateway)
				continue
			}

			lggr.Debugw("connected successfully")
			return gateway, nil
		}
	}
}

func (p *gatewayOutboundProxy) gatewayIDsForDon(ctx context.Context, donID string) ([]string, error) {
	routing, ok := p.gateway.(routingGatewayConnector)
	if !ok {
		return nil, fmt.Errorf("gateway connector does not support multi-gateway routing")
	}
	gatewayIDs, err := routing.GatewayIDsForDon(ctx, donID)
	if err != nil {
		return nil, fmt.Errorf("failed to get gateway IDs: %w", err)
	}
	if len(gatewayIDs) == 0 {
		p.metrics.IncrementNoGatewaysAvailable(ctx, donID, p.lggr)
		return nil, fmt.Errorf("no gateways configured for DON %q", donID)
	}
	return gatewayIDs, nil
}

// attemptGatewayConnection waits to connect to a gateway with a new child context
func (p *gatewayOutboundProxy) attemptGatewayConnection(ctx context.Context, lggr logger.Logger, gateway string, timeout time.Duration) error {
	lggr.Debugw("awaiting connection", "timeout", timeout)

	// create a new child context to wait on gateway connection
	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := p.gateway.AwaitConnection(ctxWithTimeout, gateway); err != nil {
		return fmt.Errorf("gateway connection failed: %w", err)
	}
	return nil
}

func (p *gatewayOutboundProxy) ID(ctx context.Context) (string, error) {
	return p.Name(), nil
}

func (p *gatewayOutboundProxy) Start(ctx context.Context) error {
	p.lggr.Debug("Starting GatewayOutboundProxy...")
	return p.StartOnce("GatewayOutboundProxy", func() error {
		// Nothing to register: what this asks the gateway for comes back on the request
		// it asked with, rather than as a message this node has to be listening for.
		return nil
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

// nextBackoff calculates the next backoff duration using the configured multiplier and max elapsed time.
func (p *gatewayOutboundProxy) nextBackoff(backoff time.Duration) time.Duration {
	backoffMs := float64(backoff.Milliseconds())
	backoffMs = backoffMs * p.gatewayConnectionConfig.Multiplier
	backoffMs = math.Min(backoffMs, float64(p.gatewayConnectionConfig.MaxElapsedTimeMs))
	return time.Duration(backoffMs) * time.Millisecond
}

// setupRing initializes a consistent hash ring with the provided nodes.
func setupRing(gatewayIDs []string) *consistent.Consistent {
	c := consistent.New()
	for _, node := range gatewayIDs {
		c.Add(node)
	}
	return c
}
