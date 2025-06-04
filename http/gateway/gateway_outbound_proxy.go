package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/pkg/errors"

	"github.com/smartcontractkit/capabilities/http/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	gc "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const (
	internalError = "internal error"

	defaultGlobalRPS          = 100.0
	defaultGlobalBurst        = 100
	defaultPerSenderRPS       = 100.0
	defaultPerSenderBurst     = 100
	defaultWorkflowOwnerRPS   = 5.0
	defaultWorkflowOwnerBurst = 50

	defaultGatewayConnectionInitialIntervalMs = 100    // 100 milliseconds
	defaultGatewayConnectionMaxElapsedTimeMs  = 30_000 // 30 seconds
	defaultGatewayConnectionMultiplier        = 2.0

	errorOutgoingRatelimitGlobal        = "global limit of outgoing gateways requests has been exceeded"
	errorOutgoingRatelimitWorkflowOwner = "workflow owner exceeded limit of gateways requests"
	errorIncomingRatelimitGlobal        = "message from gateway exceeded global rate limit"
	errorIncomingRatelimitSender        = "message from gateway exceeded per sender rate limit"
)

var _ core.GatewayConnectorHandler = &gatewayOutboundProxy{}
var _ common.OutboundRequestClient = &gatewayOutboundProxy{}

type gatewayOutboundProxy struct {
	services.StateMachine
	gatewayConnector        core.GatewayConnector
	lggr                    logger.Logger
	incomingRateLimiter     *gateway.RateLimiter
	outgoingRateLimiter     *gateway.RateLimiter
	responses               *responses
	selectorOpts            []func(*gc.RoundRobinSelector)
	gatewayConnectionConfig common.GatewayConnectionConfig
}

func NewGatewayOutboundProxy(gatewayConnector core.GatewayConnector, config common.ServiceConfig, lgger logger.Logger, opts ...func(*gc.RoundRobinSelector)) (*gatewayOutboundProxy, error) {
	outgoingRLCfg := outgoingRateLimiterConfigDefaults(config.OutgoingRateLimiter)
	outgoingRateLimiter, err := gateway.NewRateLimiter(outgoingRLCfg)
	if err != nil {
		return nil, err
	}
	incomingRLCfg := incomingRateLimiterConfigDefaults(config.RateLimiter)
	incomingRateLimiter, err := gateway.NewRateLimiter(incomingRLCfg)
	if err != nil {
		return nil, err
	}

	initialInterval := config.GatewayConnectionConfig.InitialIntervalMs
	if initialInterval == 0 {
		initialInterval = defaultGatewayConnectionInitialIntervalMs
	}
	maxElapsedTime := config.GatewayConnectionConfig.MaxElapsedTimeMs
	if maxElapsedTime == 0 {
		maxElapsedTime = defaultGatewayConnectionMaxElapsedTimeMs
	}
	multiplier := config.GatewayConnectionConfig.Multiplier
	if multiplier == 0 {
		multiplier = defaultGatewayConnectionMultiplier
	}

	return &gatewayOutboundProxy{
		gatewayConnector:    gatewayConnector,
		responses:           newResponses(),
		outgoingRateLimiter: outgoingRateLimiter,
		incomingRateLimiter: incomingRateLimiter,
		lggr:                lgger,
		selectorOpts:        opts,
		gatewayConnectionConfig: common.GatewayConnectionConfig{
			InitialIntervalMs: initialInterval,
			MaxElapsedTimeMs:  maxElapsedTime,
			Multiplier:        multiplier,
		},
	}, nil
}

// HandleSingleNodeRequest sends a request to first available gateway node and blocks until response is received
func (p *gatewayOutboundProxy) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Inputs) (*http.Outputs, error) {
	messageID := p.getMessageID(metadata)
	lggr := logger.With(p.lggr, "messageID", messageID, "workflowID", metadata.WorkflowID, "workflowExecutionID", metadata.WorkflowExecutionID, "workflowOwner", metadata.WorkflowOwner)

	ctx, cancel := context.WithTimeout(ctx, time.Duration(input.TimeoutMs)*time.Millisecond)
	defer cancel()

	workflowAllow, globalAllow := p.outgoingRateLimiter.AllowVerbose(metadata.WorkflowOwner)
	if !workflowAllow {
		return nil, errors.New(errorOutgoingRatelimitWorkflowOwner)
	}
	if !globalAllow {
		return nil, errors.New(errorOutgoingRatelimitGlobal)
	}

	gatewayReq := gc.GatewayRequest{
		WorkflowID: metadata.WorkflowID,
		URL:        input.Url,
		Method:     input.Method,
		Headers:    input.Headers,
		Body:       input.Body,
		// Casting is safe because input to this function is already validated
		TimeoutMs: uint32(input.TimeoutMs), //nolint:gosec // G115
	}

	payload, err := json.Marshal(gatewayReq)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal fetch request: %w", err)
	}

	responseCh, err := p.responses.new(messageID)
	if err != nil {
		return nil, fmt.Errorf("duplicate message received for ID: %s", messageID)
	}
	defer p.responses.cleanup(messageID)

	lggr.Debugw("sending request to gateway")

	donID, err := p.gatewayConnector.DonID()
	if err != nil {
		return nil, fmt.Errorf("failed to get DON ID: %w", err)
	}

	body := &gc.MessageBody{
		MessageId: messageID,
		DonId:     donID,
		Method:    gc.MethodHTTPAction,
		Payload:   payload,
	}

	selectedGateway, err := p.awaitConnection(ctx, lggr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to await connection to gateway")
	}

	if err := p.gatewayConnector.SignAndSendToGateway(ctx, selectedGateway, body); err != nil {
		return nil, errors.Wrap(err, "failed to send request to gateway")
	}

	select {
	case resp := <-responseCh:
		lggr.Debugw("received response from gateway")
		var gatewayResp gc.GatewayResponse
		err := json.Unmarshal(resp.Body.Payload, &gatewayResp)
		if err != nil {
			lggr.Errorw("failed to unmarshal gateway response", "error", err)
			return nil, errors.New(internalError)
		}
		if gatewayResp.ExecutionError {
			if isRateLimitError(gatewayResp.ErrorMessage) {
				return nil, errors.New(gatewayResp.ErrorMessage)
			}
			lggr.Errorw("gateway response indicates execution error", "errorMessage", gatewayResp.ErrorMessage)
			return nil, errors.New(internalError)
		}
		return &http.Outputs{
			StatusCode:   uint32(gatewayResp.StatusCode), //nolint:gosec // G115
			Headers:      gatewayReq.Headers,
			Body:         gatewayResp.Body,
			ErrorMessage: gatewayResp.ErrorMessage,
		}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TODO: all nodes must generate the same messageID for the same request if OneAtATime is used
func (p *gatewayOutboundProxy) getMessageID(metadata capabilities.RequestMetadata) string {
	messageID := []string{
		metadata.WorkflowID,
		metadata.WorkflowExecutionID,
		uuid.New().String(),
	}
	return strings.Join(messageID, "/")
}

// awaitConnection attempts to establish a connection to an available gateway.  It iterates through available gateways
// using a round robin selector, connecting to the first available.  The method respects the provided context, allowing for
// cancellation or timeout.
func (p *gatewayOutboundProxy) awaitConnection(ctx context.Context, lggr logger.Logger) (string, error) {
	gatewayIDs, err := p.gatewayConnector.GatewayIDs()
	if err != nil {
		return "", fmt.Errorf("failed to get gateway IDs: %w", err)
	}
	selector := gc.NewRoundRobinSelector(gatewayIDs, p.selectorOpts...)
	attempts := make(map[string]int)
	backoff := time.Duration(p.gatewayConnectionConfig.InitialIntervalMs) * time.Millisecond

	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
			gateway, err := selector.NextGateway()
			if err != nil {
				return "", fmt.Errorf("failed to select gateway: %w", err)
			}
			lggr = logger.With(lggr, "gateway", gateway)

			if attempts[gateway] > 0 {
				lggr.Warnw("all available gateway nodes attempted without connection, backing off", "waitTime", backoff)

				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(backoff):
					// backoff completed, update state and continue with next iteration
					attempts = make(map[string]int)
					backoff = p.nextBackoff(backoff)
				}
			}

			attempts[gateway]++

			lggr.Infow("selected gateway, awaiting connection", "selectedGateway", gateway)

			if err := p.attemptGatewayConnection(ctx, lggr, gateway, backoff); err != nil {
				lggr.Warnw("failed to await connection to gateway node, retrying", "selectedGateway", gateway, "error", err)
				continue
			}

			lggr.Debugw("connected successfully", "selectedGateway", gateway)
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
func (p *gatewayOutboundProxy) HandleGatewayMessage(ctx context.Context, gatewayID string, msg *gc.Message) error {
	l := logger.With(p.lggr, "gatewayID", gatewayID, "method", msg.Body.Method, "messageID", msg.Body.MessageId)

	ch, ok := p.responses.get(msg.Body.MessageId)
	if !ok {
		l.Warnw("no response channel found; this may indicate that the node timed out the request")
		return nil
	}

	senderAllow, globalAllow := p.incomingRateLimiter.AllowVerbose(msg.Body.Sender)
	errorMsg := ""
	if !senderAllow {
		errorMsg = errorIncomingRatelimitSender
	} else if !globalAllow {
		errorMsg = errorIncomingRatelimitGlobal
	}
	if errorMsg != "" {
		l.Errorw("request rate-limited")
		resp := gc.GatewayResponse{
			ErrorMessage:   errorMsg,
			ExecutionError: true,
		}
		payload, err := json.Marshal(resp)
		if err != nil {
			l.Errorw("failed to marshal err payload", "err", err)
			// return nil and skip processing this gateway message
			return nil
		}
		msg.Body.Payload = payload
	}

	l.Debugw("handling gateway request")
	switch msg.Body.Method {
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

// isRateLimitError checks if the error string contains any of the rate limit error constants.
func isRateLimitError(errStr string) bool {
	return strings.Contains(errStr, errorOutgoingRatelimitGlobal) ||
		strings.Contains(errStr, errorOutgoingRatelimitWorkflowOwner) ||
		strings.Contains(errStr, errorIncomingRatelimitGlobal) ||
		strings.Contains(errStr, errorIncomingRatelimitSender)
}

func (p *gatewayOutboundProxy) ID() (string, error) {
	return p.Name(), nil
}

func (p *gatewayOutboundProxy) Start(ctx context.Context) error {
	return p.StartOnce("GatewayOutboundProxy", func() error {
		return p.gatewayConnector.AddHandler([]string{gc.MethodHTTPAction}, p)
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

func incomingRateLimiterConfigDefaults(config gateway.RateLimiterConfig) gateway.RateLimiterConfig {
	if config.GlobalBurst == 0 {
		config.GlobalBurst = defaultGlobalBurst
	}
	if config.GlobalRPS == 0 {
		config.GlobalRPS = defaultGlobalRPS
	}
	if config.PerSenderBurst == 0 {
		config.PerSenderBurst = defaultPerSenderBurst
	}
	if config.PerSenderRPS == 0 {
		config.PerSenderRPS = defaultPerSenderRPS
	}
	return config
}
func outgoingRateLimiterConfigDefaults(config gateway.RateLimiterConfig) gateway.RateLimiterConfig {
	if config.GlobalBurst == 0 {
		config.GlobalBurst = defaultGlobalBurst
	}
	if config.GlobalRPS == 0 {
		config.GlobalRPS = defaultGlobalRPS
	}
	if config.PerSenderBurst == 0 {
		config.PerSenderBurst = defaultWorkflowOwnerBurst
	}
	if config.PerSenderRPS == 0 {
		config.PerSenderRPS = defaultWorkflowOwnerRPS
	}
	return config
}

func newResponses() *responses {
	return &responses{
		chs: map[string]chan *gc.Message{},
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
	chs map[string]chan *gc.Message
	mu  sync.RWMutex
}

func (r *responses) new(id string) (chan *gc.Message, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	_, ok := r.chs[id]
	if ok {
		return nil, fmt.Errorf("already have response for id: %s", id)
	}

	// Buffered so we don't wait if sending
	ch := make(chan *gc.Message, 1)
	r.chs[id] = ch
	return ch, nil
}

func (r *responses) cleanup(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.chs, id)
}

func (r *responses) get(id string) (chan *gc.Message, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.chs[id]
	return ch, ok
}
