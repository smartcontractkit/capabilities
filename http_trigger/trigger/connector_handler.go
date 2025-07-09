package trigger

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/ethereum/go-ethereum/accounts/abi"

	gethcommon "github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/workflow/generated/workflow_registry_wrapper_v2"
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
var registryABI = MustParseABI(workflow_registry_wrapper_v2.WorkflowRegistryABI)

type connectorHandler struct {
	services.StateMachine
	lggr                 logger.Logger
	gatewayConnector     core.GatewayConnector
	workflowsMu          sync.RWMutex
	workflows            map[string]*workflow // workflowID -> workflow metadata
	config               ServiceConfig
	incomingRateLimiter  *ratelimit.RateLimiter
	outgoingRateLimiter  *ratelimit.RateLimiter
	evm                  types.EVMService
	workflowRegistryAddr gethcommon.Address
}

func NewConnectorHandler(lggr logger.Logger, gc core.GatewayConnector, config ServiceConfig, evmService types.EVMService) (*connectorHandler, error) {
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
	workflowRegistryAddr := gethcommon.HexToAddress(config.WorkflowRegistryAddress)
	if workflowRegistryAddr == (gethcommon.Address{}) {
		return nil, fmt.Errorf("invalid workflow registry address: %s", config.WorkflowRegistryAddress)
	}
	return &connectorHandler{
		lggr:                 logger.Named(lggr, HandlerName),
		gatewayConnector:     gc,
		config:               config,
		outgoingRateLimiter:  outgoingRateLimiter,
		incomingRateLimiter:  incomingRateLimiter,
		workflows:            make(map[string]*workflow),
		workflowRegistryAddr: workflowRegistryAddr,
		evm:                  evmService,
	}, nil
}

func (h *connectorHandler) Start(ctx context.Context) error {
	h.lggr.Debug("Starting request handler")
	return h.StartOnce(HandlerName, func() error {
		return h.gatewayConnector.AddHandler(ctx, []string{gateway_common.MethodWorkflowExecute}, h)
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

func (h *connectorHandler) RegisterWorkflow(ctx context.Context, workflowID string, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	authorizedKeys := map[string]struct{}{}
	for _, key := range input.AuthorizedKeys {
		switch key.Type {
		case http.KeyType_KEY_TYPE_ECDSA:
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
	h.workflows[workflowID] = newWorkflow(authorizedKeys, sendCh)
	h.lggr.Debugf("Registered workflow %s", workflowID)
	return nil
}

func (h *connectorHandler) UnregisterWorkflow(ctx context.Context, workflowID string) error {
	h.workflowsMu.Lock()
	defer h.workflowsMu.Unlock()
	workflow, ok := h.workflows[workflowID]
	if !ok {
		return fmt.Errorf("workflowID %s not registered", workflowID)
	}
	workflow.close()
	delete(h.workflows, workflowID)
	h.lggr.Debugf("Unregistered workflow %s", workflowID)
	return nil
}

// HandleGatewayMessage processes incoming messages from gateways.
// Always returns nil. Unless request is malformed or rate-limited, response is sent back to the
// gateway using sendResponse method.
func (h *connectorHandler) HandleGatewayMessage(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error {
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
	default:
		h.lggr.Errorw("Unsupported method", "method", req.Method, "gatewayID", gatewayID)
	}
	return nil
}

func (h *connectorHandler) sendErrorResponse(ctx context.Context, gatewayID string, reqID string, code int64, message string) {
	resp := &jsonrpc.Response[json.RawMessage]{
		Version: "2.0",
		ID:      reqID,
		Error: &jsonrpc.WireError{
			Code:    code,
			Message: message,
		},
	}
	h.sendResponse(ctx, gatewayID, resp)
}

func (h *connectorHandler) sendResponse(ctx context.Context, gatewayID string, resp *jsonrpc.Response[json.RawMessage]) {
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

func (h *connectorHandler) fetchWorkflowByTag(ctx context.Context, gatewayID string, requestID string, workflowOwner string, workflowName string, tag string) (string, error) {
	if workflowOwner == "" || workflowName == "" || tag == "" {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInvalidRequest, "Workflow not found")
		return "", errors.New("workflowOwner, workflowName, and tag must be provided")
	}
	workflowOwnerAddr := gethcommon.HexToAddress(workflowOwner)
	if workflowOwnerAddr == (gethcommon.Address{}) {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInvalidRequest, "Workflow not found")
		return "", errors.New("invalid workflowOwner address")
	}
	calldata, err := registryABI.Pack("getWorkflow", workflowOwnerAddr, workflowName, tag)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInternal, "Failed to pack ABI call")
		return "", fmt.Errorf("failed to pack ABI call: %w", err)
	}
	msg := &evm.CallMsg{
		To:   h.workflowRegistryAddr,
		Data: calldata,
	}
	data, err := h.evm.CallContract(ctx, msg, nil)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInternal, "Failed to call contract")
		return "", fmt.Errorf("failed to call contract: %w", err)
	}
	method, ok := registryABI.Methods["getWorkflow"]
	if !ok {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInternal, "Missing method getWorkflow in ABI")
		return "", errors.New("missing method getWorkflow")
	}
	unpacked, err := method.Outputs.Unpack(data)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInternal, "Failed to unpack ABI response")
		return "", fmt.Errorf("failed to unpack: %w", err)
	}
	if len(unpacked) != 1 {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInternal, "Failed to unpack ABI response: expected 1 output")
		return "", fmt.Errorf("expected 1 argument, got %d", len(unpacked))
	}
	workflowMetadataView := *abi.ConvertType(unpacked[0], new(workflow_registry_wrapper_v2.WorkflowRegistryWorkflowMetadataView)).(*workflow_registry_wrapper_v2.WorkflowRegistryWorkflowMetadataView)
	if workflowMetadataView.WorkflowId == (gethcommon.Hash{}) {
		h.sendErrorResponse(ctx, gatewayID, requestID, jsonrpc.ErrInvalidRequest, "Workflow not found")
		return "", fmt.Errorf("workflow not found for owner %s, name %s, tag %s", workflowOwner, workflowName, tag)
	}
	return "0x" + hex.EncodeToString(workflowMetadataView.WorkflowId[:]), nil
}

func (h *connectorHandler) processTrigger(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) {
	var triggerReq gateway_common.HTTPTriggerRequest
	if req.Params == nil {
		req.Params = &json.RawMessage{}
	}

	err := json.Unmarshal(*req.Params, &triggerReq)
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
	workflowID := triggerReq.Workflow.WorkflowID
	if workflowID == "" {
		workflowID, err = h.fetchWorkflowByTag(ctx, gatewayID, req.ID, triggerReq.Workflow.WorkflowOwner, triggerReq.Workflow.WorkflowName, triggerReq.Workflow.WorkflowLabel)
		if err != nil {
			// error is sent back to the gateway inside fetchWorkflowByTag
			l.Errorw("Failed to fetch workflow", "error", err)
			return
		}
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
	payloadMsg := json.RawMessage(jsonPayload)
	resp := &jsonrpc.Response[json.RawMessage]{
		Version: "2.0",
		ID:      req.ID,
		Result:  &payloadMsg,
	}
	h.sendResponse(ctx, gatewayID, resp)
}

func (h *connectorHandler) triggerWorkflow(ctx context.Context, workflowID string, reqID string, gatewayID string, input *structpb.Struct) error {
	h.workflowsMu.RLock()
	workflow, ok := h.workflows[workflowID]
	h.workflowsMu.RUnlock()
	if !ok {
		h.sendErrorResponse(ctx, gatewayID, reqID, jsonrpc.ErrInvalidRequest, "Workflow not registered")
		return fmt.Errorf("workflowID %s not registered", workflowID)
	}
	err := workflow.trigger(ctx, capabilities.TriggerAndId[*http.Payload]{
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

func MustParseABI(abiStr string) abi.ABI {
	abiParsed, err := abi.JSON(strings.NewReader(abiStr))
	if err != nil {
		panic(err)
	}
	return abiParsed
}
