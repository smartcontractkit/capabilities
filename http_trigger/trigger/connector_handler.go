package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
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

var _ core.GatewayConnectorHandler = &connectorHandler{}

type connectorHandler struct {
	services.StateMachine
	lggr                  logger.Logger
	gatewayConnector      core.GatewayConnector
	config                ServiceConfig
	incomingRateLimiter   *ratelimit.RateLimiter
	outgoingRateLimiter   *ratelimit.RateLimiter
	authMetadataHandler   AuthMetadataHandler
	workflowMetadataStore WorkflowMetadataStore
}

type AuthMetadataHandler interface {
	// HandlePullFromGateway processes a request to pull authentication metadata from the gateway.
	// It is expected to send a response back to the gateway using the gatewayConnector.
	// In case of an error, it should log and send an error response back to the gateway
	HandlePullFromGateway(ctx context.Context, gatewayID string, req *jsonrpc.Request)
	// PushToGateway sends the authentication metadata to the gateway.
	PushToGateway(ctx context.Context, workflowID string, keys []AuthorizedKey) error
}

type WorkflowMetadataStore interface {
	RegisterWorkflow(workflowID string, authorizedKeys []AuthorizedKey, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error
	UnregisterWorkflow(workflowID string) error
	GetWorkflow(workflowID string) (*workflow, error)
}

func NewConnectorHandler(lggr logger.Logger, gc core.GatewayConnector, config ServiceConfig, workflowMetadataStore WorkflowMetadataStore, authMetadataHandler AuthMetadataHandler) (*connectorHandler, error) {
	outgoingRLCfg := outgoingRateLimiterConfigDefaults(config.OutgoingRateLimiter)
	outgoingRateLimiter, err := ratelimit.NewRateLimiter(outgoingRLCfg)
	if err != nil {
		return nil, err
	}
	incomingRLCfg := incomingRateLimiterConfigDefaults(config.RateLimiter)
	incomingRateLimiter, err := ratelimit.NewRateLimiter(incomingRLCfg)
	if err != nil {
		return nil, err
	}
	return &connectorHandler{
		lggr:                  logger.Named(lggr, HandlerName),
		gatewayConnector:      gc,
		config:                config,
		outgoingRateLimiter:   outgoingRateLimiter,
		incomingRateLimiter:   incomingRateLimiter,
		workflowMetadataStore: workflowMetadataStore,
	}, nil
}

func (h *connectorHandler) Start(ctx context.Context) error {
	h.lggr.Debug("Starting request handler")
	return h.StartOnce(HandlerName, func() error {
		return h.gatewayConnector.AddHandler(ctx, []string{
			gateway_common.MethodWorkflowExecute,
			MethodWorkflowPushAuthMetadata,
			MethodWorkflowPullAuthMetadata,
		}, h)
	})
}

func (h *connectorHandler) Close() error {
	h.lggr.Debug("Stopping request handler")
	return h.StopOnce(HandlerName, func() error {
		return nil
	})
}

func (h *connectorHandler) HealthReport() map[string]error {
	return map[string]error{h.Name(): h.Healthy()}
}

func (h *connectorHandler) Ready() error {
	return h.StateMachine.Healthy()
}

func (h *connectorHandler) Name() string {
	return HandlerName
}

func (h *connectorHandler) ID(context.Context) (string, error) {
	return HandlerName, nil
}

func (h *connectorHandler) RegisterWorkflow(ctx context.Context, workflowID string, triggerID string, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	var authorizedKeys []AuthorizedKey
	for _, key := range input.AuthorizedKeys {
		switch key.Type {
		case http.KeyType_ECDSA:
			if len(key.PublicKey) != ecdsaPubKeyHexLen || key.PublicKey[:2] != "0x" {
				return fmt.Errorf("invalid public key format: must be 0x-prefixed hex string of length %d, got %q", ecdsaPubKeyHexLen, key.PublicKey)
			}
			authorizedKeys = append(authorizedKeys, AuthorizedKey{
				KeyType:   KeyTypeECDSA,
				PublicKey: key.PublicKey,
			})
		default:
			return fmt.Errorf("unsupported key type: %s", key.Type)
		}
	}
	err := h.workflowMetadataStore.RegisterWorkflow(workflowID, authorizedKeys, sendCh)
	if err != nil {
		return errors.Join(err, fmt.Errorf("failed to register workflow %s: %w", workflowID, err))
	}
	// Push the auth metadata to the gateway
	// Error is non-critical. Retries will be handled by the authMetadataHandler.
	err = h.authMetadataHandler.PushToGateway(ctx, workflowID, triggerID, authorizedKeys)
	if err != nil {
		h.lggr.Errorw("Failed to push auth metadata to gateway", "error",
			err, "workflowID", workflowID, "triggerID", triggerID)
	}
	return nil
}

func (h *connectorHandler) UnregisterWorkflow(ctx context.Context, workflowID string) error {
	return h.workflowMetadataStore.UnregisterWorkflow(workflowID)
}

// HandleGatewayMessage processes incoming messages from gateways.
// Always returns nil. Unless request is malformed or rate-limited, response is sent back to the
// gateway using sendResponse method.
func (h *connectorHandler) HandleGatewayMessage(ctx context.Context, gatewayID string, req *jsonrpc.Request) error {
	senderAllow, globalAllow := h.incomingRateLimiter.AllowVerbose(gatewayID)
	if !senderAllow {
		h.lggr.Errorw(errorIncomingRatelimitSender, "gatewayID", gatewayID)
		return nil
	}
	if !globalAllow {
		h.lggr.Errorw(errorIncomingRatelimitGlobal, "gatewayID", gatewayID)
		return nil
	}

	switch req.Method {
	case gateway_common.MethodWorkflowExecute:
		h.processTrigger(ctx, gatewayID, req)
	case MethodWorkflowPullAuthMetadata:
		h.authMetadataHandler.HandlePullFromGateway(ctx, gatewayID, req)
	default:
		h.lggr.Errorw("Unsupported method", "method", req.Method, "gatewayID", gatewayID)
	}
	return nil
}

func (h *connectorHandler) sendErrorResponse(ctx context.Context, gatewayID string, reqID string, code int64, message string) {
	resp := &jsonrpc.Response{
		Version: "2.0",
		ID:      reqID,
		Error: &jsonrpc.WireError{
			Code:    code,
			Message: message,
		},
	}
	h.sendResponse(ctx, gatewayID, resp)
}

func (h *connectorHandler) sendResponse(ctx context.Context, gatewayID string, resp *jsonrpc.Response) {
	senderAllow, globalAllow := h.outgoingRateLimiter.AllowVerbose(gatewayID)
	if !senderAllow {
		h.lggr.Errorw(errorOutgoingRatelimitSender, "gatewayID", gatewayID)
		return
	}
	if !globalAllow {
		h.lggr.Errorw(errorOutgoingRatelimitGlobal, "gatewayID", gatewayID)
		return
	}
	err := h.gatewayConnector.SendToGateway(ctx, gatewayID, resp)
	if err != nil {
		h.lggr.Errorw("Failed to send response to gateway", "error", err, "gatewayID", gatewayID)
		return
	}
}

func (h *connectorHandler) processTrigger(ctx context.Context, gatewayID string, req *jsonrpc.Request) {
	var triggerReq gateway_common.HTTPTriggerRequest
	err := json.Unmarshal(req.Params, &triggerReq)
	if err != nil {
		h.lggr.Errorw("Failed to unmarshal HTTP trigger request", "error", err, "gatewayID", gatewayID, "requestID", req.ID)
		return
	}
	l := logger.With(h.lggr, "gatewayID", gatewayID, "requestID", req.ID, "method", req.Method)
	input, err := convertRawJSONToProto(triggerReq.Input)
	if err != nil {
		l.Errorw("Failed to convert input JSON to proto", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrParse, "Invalid input JSON")
		return
	}
	// TODO: PRODCRE-305 validate JWT against authorized keys
	// TODO: PRODCRE-475 support look-up of workflowID using workflowOwner/Label/Name
	workflowID := triggerReq.Workflow.WorkflowID
	if workflowID == "" {
		l.Error("WorkflowID is required in HTTP trigger request")
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInvalidParams, "workflowID is required")
		return
	}
	l = logger.With(l, "workflowID", workflowID)
	err = h.triggerWorkflow(ctx, workflowID, req.ID, gatewayID, input)
	if err != nil {
		l.Errorw("Failed to trigger workflow", "error", err)
		return
	}
	workflowExecutionID, err := workflows.EncodeExecutionID(workflowID, req.ID)
	if err != nil {
		l.Errorw("Failed to generate workflow execution ID", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return
	}
	payload := &gateway_common.HTTPTriggerResponse{
		WorkflowID:          workflowID,
		WorkflowExecutionID: workflowExecutionID,
		Status:              gateway_common.HTTPTriggerStatusAccepted,
	}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		l.Errorw("Failed to marshal HTTP trigger response", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return
	}
	resp := &jsonrpc.Response{
		Version: "2.0",
		ID:      req.ID,
		Result:  jsonPayload,
	}
	h.sendResponse(ctx, gatewayID, resp)
}

func (h *connectorHandler) triggerWorkflow(ctx context.Context, workflowID string, reqID string, gatewayID string, input *structpb.Struct) error {
	workflow, err := h.workflowMetadataStore.GetWorkflow(workflowID)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, reqID, jsonrpc.ErrInvalidRequest, "Workflow not registered")
		return fmt.Errorf("workflowID %s not registered", workflowID)
	}
	err = workflow.trigger(ctx, capabilities.TriggerAndId[*http.Payload]{
		// workflow engine does not process the request if the ID has already been used
		Id: reqID,
		Trigger: &http.Payload{
			Input: input,
			// TODO: PRODCRE-305 validate JWT against authorized keys
		},
	})
	if err != nil {
		if errors.Is(err, errWorkflowClosed) {
			h.sendErrorResponse(ctx, gatewayID, reqID, jsonrpc.ErrInvalidRequest, err.Error())
		} else if errors.Is(err, errFullChannel) {
			h.sendErrorResponse(ctx, gatewayID, reqID, jsonrpc.ErrServerOverloaded, err.Error())
		}
		return err
	}
	return nil
}

func incomingRateLimiterConfigDefaults(config ratelimit.RateLimiterConfig) ratelimit.RateLimiterConfig {
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
func outgoingRateLimiterConfigDefaults(config ratelimit.RateLimiterConfig) ratelimit.RateLimiterConfig {
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
