package trigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const (
	HandlerName                  = "HTTPTriggerHandler"
	defaultGlobalRPS             = 100.0
	defaultGlobalBurst           = 100
	defaultPerSenderRPS          = 100.0
	defaultPerSenderBurst        = 100
	errorOutgoingRatelimitGlobal = "global limit of outgoing gateways requests has been exceeded"
	errorOutgoingRatelimitSender = "per-sender limit of outgoing gateways requests has been exceeded"
	errorIncomingRatelimitGlobal = "message from gateway exceeded global rate limit"
	errorIncomingRatelimitSender = "message from gateway exceeded per sender rate limit"
)

// TODO: move these to common package
const MethodHTTPTrigger = "http_trigger"

var _ core.GatewayConnectorHandler = &requestHandler{}

type requestHandler struct {
	services.StateMachine
	lggr                logger.Logger
	gatewayConnector    core.GatewayConnector
	workflowsMu         sync.RWMutex
	workflows           map[string]workflow // workflowID -> workflow
	config              ServiceConfig
	incomingRateLimiter *gateway.RateLimiter
	outgoingRateLimiter *gateway.RateLimiter
}

type workflow struct {
	authorizedKeys map[string]struct{}
	sendCh         chan<- capabilities.TriggerAndId[*http.Payload]
}

func NewRequestHandler(lggr logger.Logger, gc core.GatewayConnector, config ServiceConfig) (*requestHandler, error) {
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
	return &requestHandler{
		lggr:                logger.Named(lggr, HandlerName),
		gatewayConnector:    gc,
		config:              config,
		outgoingRateLimiter: outgoingRateLimiter,
		incomingRateLimiter: incomingRateLimiter,
	}, nil
}

func (h *requestHandler) Start(context.Context) error {
	h.lggr.Debug("Starting request handler")
	return h.StartOnce(HandlerName, func() error {
		return h.gatewayConnector.AddHandler([]string{MethodHTTPTrigger}, h)
	})
}

func (h *requestHandler) Close() error {
	return h.StopOnce(HandlerName, func() error {
		return nil
	})
}

func (h *requestHandler) ID() (string, error) {
	return HandlerName, nil
}

func (h *requestHandler) RegisterWorkflow(ctx context.Context, workflowID string, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	authorizedKeys := map[string]struct{}{}
	for _, key := range input.AuthorizedKeys {
		if key.GetEcdsa() != nil {
			authorizedKeys[key.GetEcdsa().PublicKey] = struct{}{}
		} else {
			return fmt.Errorf("unexpected key type: %T", key)
		}
	}

	h.workflowsMu.Lock()
	defer h.workflowsMu.Unlock()
	_, ok := h.workflows[workflowID]
	if ok {
		h.lggr.Debugw("Workflow already registered, re-registering", "workflowID", workflowID)
	}
	h.workflows[workflowID] = workflow{
		authorizedKeys: authorizedKeys,
		sendCh:         sendCh,
	}
	h.lggr.Debugf("Registered workflow %s", workflowID)
	return nil
}

func (h *requestHandler) UnregisterWorkflow(ctx context.Context, workflowID string) error {
	h.workflowsMu.Lock()
	defer h.workflowsMu.Unlock()
	workflow, ok := h.workflows[workflowID]
	if !ok {
		return fmt.Errorf("workflowID %s not registered", workflowID)
	}
	close(workflow.sendCh)
	delete(h.workflows, workflowID)
	h.lggr.Debugf("Unregistered workflow %s", workflowID)
	return nil
}

func (h *requestHandler) HandleGatewayMessage(ctx context.Context, gatewayID string, msg *gateway.Message) error {
	senderAllow, globalAllow := h.incomingRateLimiter.AllowVerbose(gatewayID)
	if !senderAllow {
		return errors.New(errorIncomingRatelimitSender)
	}
	if !globalAllow {
		return errors.New(errorIncomingRatelimitGlobal)
	}

	switch msg.Body.Method {
	case MethodHTTPTrigger:
		// TODO: handle error response
		return h.processTrigger(ctx, gatewayID, msg)
	default:
		return fmt.Errorf("unsupported method %s", msg.Body.Method)
	}
}

func (h *requestHandler) processTrigger(ctx context.Context, gatewayID string, msg *gateway.Message) error {
	var req WrappedHTTPTriggerRequest
	err := json.Unmarshal(msg.Body.Payload, &req)
	if err != nil {
		return fmt.Errorf("failed to unmarshal HTTP trigger request: %w", err)
	}
	var triggerReq HTTPTriggerRequest
	err = json.Unmarshal(req.ReqBody.Params, &triggerReq)
	if err != nil {
		return fmt.Errorf("failed to unmarshal HTTP trigger request body: %w", err)
	}
	// TODO: validate JWT
	h.workflowsMu.RLock()
	workflow, ok := h.workflows[req.WorkflowID]
	h.workflowsMu.RUnlock()
	if !ok {
		return fmt.Errorf("workflow %s not registered", req.WorkflowID)
	}
	workflowExecutionID, err := generateExecutionID(req.WorkflowID, triggerReq.DeduplicationKey)
	if err != nil {
		return fmt.Errorf("failed to generate execution ID: %w", err)
	}
	payload := HTTPTriggerResponse{
		Status:              HTTPTriggerStatusAccepted,
		WorkflowID:          req.WorkflowID,
		WorkflowExecutionID: workflowExecutionID,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal HTTP trigger response: %w", err)
	}
	senderAllow, globalAllow := h.outgoingRateLimiter.AllowVerbose(gatewayID)
	if !senderAllow {
		return errors.New(errorOutgoingRatelimitSender)
	}
	if !globalAllow {
		return errors.New(errorOutgoingRatelimitSender)
	}
	input, err := convertRawJSONToProto(triggerReq.Input)
	if err != nil {
		return fmt.Errorf("failed to convert input to proto struct: %w", err)
	}
	workflow.sendCh <- capabilities.TriggerAndId[*http.Payload]{
		Id: triggerReq.DeduplicationKey,
		Trigger: &http.Payload{
			Input: input,
			// Key: // TODO:
		},
	}
	return h.gatewayConnector.SignAndSendToGateway(context.Background(), gatewayID, &gateway.MessageBody{
		MessageId: msg.Body.MessageId,
		Method:    MethodHTTPTrigger,
		Payload:   jsonPayload,
	})
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
		config.PerSenderBurst = defaultPerSenderBurst
	}
	if config.PerSenderRPS == 0 {
		config.PerSenderRPS = defaultPerSenderRPS
	}
	return config
}

func convertRawJSONToProto(raw json.RawMessage) (*structpb.Struct, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal raw JSON: %w", err)
	}

	s, err := structpb.NewStruct(m)
	if err != nil {
		return nil, fmt.Errorf("failed to convert map to structpb.Struct: %w", err)
	}
	return s, nil
}

// TODO: move this to chainlink-common
type WrappedHTTPTriggerRequest struct {
	AuthToken  string
	ReqBody    JSONRPCRequest
	WorkflowID string
}

type HTTPTriggerRequest struct {
	Workflow         WorkflowSelector `json:"workflow"`
	DeduplicationKey string           `json:"deduplicationKey"`
	Input            json.RawMessage  `json:"input"`
}

type WorkflowSelector struct {
	WorkflowOwner string `json:"workflowOwner,omitempty"`
	WorkflowName  string `json:"workflowName,omitempty"`
	WorkflowLabel string `json:"workflowLabel,omitempty"`
	WorkflowID    string `json:"workflowID,omitempty"`
}

type HTTPTriggerResponse struct {
	ErrorMessage        string            `json:"error_message,omitempty"`
	Status              HTTPTriggerStatus `json:"status"`
	WorkflowID          string            `json:"workflow_id,omitempty"`
	WorkflowExecutionID string            `json:"workflow_execution_id,omitempty"`
}

type HTTPTriggerStatus string

const (
	HTTPTriggerStatusError    HTTPTriggerStatus = "ERROR"
	HTTPTriggerStatusAccepted HTTPTriggerStatus = "ACCEPTED"
)

type JSONRPCRequest struct {
	Version string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func generateExecutionID(workflowID, triggerEventID string) (string, error) {
	s := sha256.New()
	_, err := s.Write([]byte(workflowID))
	if err != nil {
		return "", err
	}

	_, err = s.Write([]byte(triggerEventID))
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(s.Sum(nil)), nil
}
