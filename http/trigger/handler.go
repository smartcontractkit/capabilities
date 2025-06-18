package trigger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/types/jsonrpc"
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
	ecdsaPubKeyHexLen            = 42 // 2 (0x prefix) + 40 (hex digits)
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
	codec               jsonrpc.Codec
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
		codec:               jsonrpc.Codec{},
		workflows:           make(map[string]workflow),
	}, nil
}

func (h *requestHandler) Start(ctx context.Context) error {
	h.lggr.Debug("Starting request handler")
	return h.StartOnce(HandlerName, func() error {
		return h.gatewayConnector.AddHandler(ctx, []string{MethodHTTPTrigger}, h)
	})
}

func (h *requestHandler) Close() error {
	return h.StopOnce(HandlerName, func() error {
		return nil
	})
}

func (h *requestHandler) ID(context.Context) (string, error) {
	return HandlerName, nil
}

func (h *requestHandler) RegisterWorkflow(ctx context.Context, workflowID string, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	authorizedKeys := map[string]struct{}{}
	for _, key := range input.AuthorizedKeys {
		switch key.Type {
		case http.KeyType_ECDSA:
			if len(key.PublicKey) != ecdsaPubKeyHexLen || key.PublicKey[:2] != "0x" {
				return fmt.Errorf("invalid public key format: must be 0x-prefixed hex string of length %d, got %q", ecdsaPubKeyHexLen, key.PublicKey)
			}
			authorizedKeys[key.PublicKey] = struct{}{}
		default:
			return fmt.Errorf("unsupported key type: %s", key.Type)
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

func (h *requestHandler) HandleGatewayMessage(ctx context.Context, gatewayID string, data []byte) error {
	req, err := h.codec.DecodeRequest(data)
	if err != nil {
		h.lggr.Errorw("Failed to decode request", "error", err, "gatewayID", gatewayID)
		return nil
	}
	senderAllow, globalAllow := h.incomingRateLimiter.AllowVerbose(gatewayID)
	if !senderAllow {
		h.lggr.Errorw(errorIncomingRatelimitSender, "gatewayID", gatewayID, "error", errorIncomingRatelimitSender)
		return nil
	}
	if !globalAllow {
		h.lggr.Errorw(errorIncomingRatelimitGlobal, "gatewayID", gatewayID, "error", errorIncomingRatelimitSender)
		return nil
	}

	switch req.Method {
	case MethodHTTPTrigger:
		h.processTrigger(ctx, gatewayID, req)
	default:
		h.lggr.Errorw("Unsupported method", "method", req.Method, "gatewayID", gatewayID)
	}
	return nil
}

func (h *requestHandler) sendErrorResponse(ctx context.Context, gatewayID string, reqID string, payload HTTPTriggerResponse, code int, message string) {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		h.lggr.Errorw("Failed to marshal error response", "error", err, "gatewayID", gatewayID)
		return
	}
	resp := jsonrpc.Response{
		Version: "2.0",
		ID:      reqID,
		Result:  payloadJSON,
		Error: &jsonrpc.Error{
			Code:    code,
			Message: message,
		},
	}
	data, err := h.codec.EncodeResponse(&resp)
	if err != nil {
		h.lggr.Errorw("Failed to encode error response", "error", err, "gatewayID", gatewayID)
		return
	}
	h.sendResponse(ctx, gatewayID, data)
}

func (h *requestHandler) sendResponse(ctx context.Context, gatewayID string, data []byte) {
	senderAllow, globalAllow := h.outgoingRateLimiter.AllowVerbose(gatewayID)
	if !senderAllow {
		h.lggr.Errorw(errorOutgoingRatelimitSender, "gatewayID", gatewayID)
		return
	}
	if !globalAllow {
		h.lggr.Errorw(errorOutgoingRatelimitGlobal, "gatewayID", gatewayID)
		return
	}
	err := h.gatewayConnector.SendToGateway(ctx, gatewayID, data)
	if err != nil {
		h.lggr.Errorw("Failed to send response to gateway", "error", err, "gatewayID", gatewayID)
		return
	}
}

func (h *requestHandler) processTrigger(ctx context.Context, gatewayID string, req jsonrpc.Request) {
	l := logger.With(h.lggr, "gatewayID", gatewayID)
	var triggerReq HTTPTriggerRequest
	err := json.Unmarshal(req.Params, &triggerReq)
	if err != nil {
		l.Errorw("Failed to unmarshal HTTP trigger request", "error", err)
		return
	}
	l = logger.With(h.lggr, "deduplicationKey", triggerReq.DeduplicationKey)
	payload := HTTPTriggerResponse{
		DeduplicationKey: triggerReq.DeduplicationKey,
	}
	input, err := convertRawJSONToProto(triggerReq.Input)
	if err != nil {
		l.Errorw("Failed to convert input JSON to proto", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, payload, CodeValidationError, "Invalid input JSON")
		return
	}
	// TODO: PRODCRE-305 validate JWT against authorized keys
	// TODO: PRODCRE-475 support look-up of workflowID using workflowOwner/Label/Name
	workflowID := triggerReq.Workflow.WorkflowID
	if workflowID == "" {
		l.Error("WorkflowID is required in HTTP trigger request")
		h.sendErrorResponse(ctx, gatewayID, req.ID, payload, CodeValidationError, "workflowID is required")
		return
	}
	l = logger.With(l, "workflowID", workflowID)
	payload.WorkflowID = workflowID
	h.workflowsMu.RLock()
	workflow, ok := h.workflows[workflowID]
	h.workflowsMu.RUnlock()
	if !ok {
		l.Error("Workflow not registered")
		h.sendErrorResponse(ctx, gatewayID, req.ID, payload, CodeResourceNotFound, "Workflow not registered")
		return
	}
	workflowExecutionID, err := generateExecutionID(workflowID, triggerReq.DeduplicationKey)
	if err != nil {
		l.Errorw("Failed to generate workflow execution ID", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, payload, CodeInternalServerError, "Internal server error")
		return
	}
	workflow.sendCh <- capabilities.TriggerAndId[*http.Payload]{
		Id: triggerReq.DeduplicationKey,
		Trigger: &http.Payload{
			Input: input,
			// TODO: PRODCRE-305 validate JWT against authorized keys
		},
	}
	payload.WorkflowExecutionID = workflowExecutionID
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		l.Errorw("Failed to marshal HTTP trigger response", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, payload, CodeInternalServerError, "Internal server error")
		return
	}
	resp, err := h.codec.EncodeResponse(&jsonrpc.Response{
		Version: "2.0",
		ID:      req.ID,
		Result:  jsonPayload,
	})
	if err != nil {
		l.Errorw("Failed to encode HTTP trigger response", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, payload, CodeInternalServerError, "Internal server error")
		return
	}
	h.sendResponse(ctx, gatewayID, resp)
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
	WorkflowID          string `json:"workflow_id,omitempty"`
	DeduplicationKey    string `json:"deduplication_key,omitempty"`
	WorkflowExecutionID string `json:"workflow_execution_id,omitempty"`
}

type HTTPTriggerStatus string

const (
	HTTPTriggerStatusError    HTTPTriggerStatus = "ERROR"
	HTTPTriggerStatusAccepted HTTPTriggerStatus = "ACCEPTED"
	CodeValidationError                         = -32602 // ValidationError: invalid fields, not retryable
	CodeUnauthorized                            = -32001 // Unauthorized: invalid/missing JWT, not retryable
	CodeResourceNotFound                        = -32004 // ResourceNotFound: workflowID does not exist, not retryable
	CodeLimitExceeded                           = -32029 // LimitExceeded: rate limits exceeded, retryable
	CodeInternalServerError                     = -32603 // InternalServerError: unexpected errors, retryable
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
