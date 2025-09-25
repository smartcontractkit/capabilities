package trigger

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	workflowsevents "github.com/smartcontractkit/chainlink-protos/workflows/go/v2"
)

const (
	HandlerName                  = "HTTPTriggerHandler"
	errorOutgoingRatelimitGlobal = "global limit of outgoing gateways requests has been exceeded"
	errorOutgoingRatelimitSender = "per-sender limit of outgoing gateways requests has been exceeded"
	errorIncomingRatelimitGlobal = "message from gateway exceeded global rate limit"
	errorIncomingRatelimitSender = "message from gateway exceeded per sender rate limit"
	ecdsaPubKeyHexLen            = 42 // 2 (0x prefix) + 40 (hex digits)
)

var _ core.GatewayConnectorHandler = &connectorHandler{}

type connectorHandler struct {
	services.StateMachine
	lggr                     logger.Logger
	gatewayConnector         core.GatewayConnector
	config                   ServiceConfig
	requestCache             *requestCache
	workflowStore            *workflowStore
	incomingRateLimiter      *ratelimit.RateLimiter
	outgoingRateLimiter      *ratelimit.RateLimiter
	gatewayMetadataPublisher GatewayMetadataPublisher
	metrics                  *Metrics
	wg                       sync.WaitGroup
	stopChan                 services.StopChan
}

func NewConnectorHandler(lggr logger.Logger, gc core.GatewayConnector, config ServiceConfig,
	outgoingRateLimiter *ratelimit.RateLimiter, incomingRateLimiter *ratelimit.RateLimiter,
	workflowStore *workflowStore, gatewayMetadataPublisher GatewayMetadataPublisher, requestCache *requestCache, metrics *Metrics) (*connectorHandler, error) {
	return &connectorHandler{
		lggr:                     logger.Named(lggr, HandlerName),
		gatewayConnector:         gc,
		config:                   config,
		outgoingRateLimiter:      outgoingRateLimiter,
		incomingRateLimiter:      incomingRateLimiter,
		workflowStore:            workflowStore,
		gatewayMetadataPublisher: gatewayMetadataPublisher,
		requestCache:             requestCache,
		metrics:                  metrics,
		stopChan:                 make(chan struct{}),
	}, nil
}

func (h *connectorHandler) Start(ctx context.Context) error {
	h.lggr.Debug("Starting request handler")
	h.wg.Add(1)
	go h.startRequestCacheCleanup(ctx)
	return h.StartOnce(HandlerName, func() error {
		return h.gatewayConnector.AddHandler(ctx, []string{
			gateway_common.MethodWorkflowExecute,
			gateway_common.MethodPullWorkflowMetadata,
			gateway_common.MethodPushWorkflowMetadata,
		}, h)
	})
}

func (h *connectorHandler) startRequestCacheCleanup(ctx context.Context) {
	defer h.wg.Done()
	ticker := time.NewTicker(h.requestCache.ttl)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopChan:
			h.lggr.Debug("Request cache cleanup routine stopping due to context cancellation")
			return
		case <-ticker.C:
			count, err := h.requestCache.cleanup(ctx)
			if err != nil {
				h.lggr.Errorw("Failed to cleanup request cache", "error", err)
			} else {
				h.lggr.Debugw("Cleaned up expired request cache entries", "interval", h.requestCache.ttl, "count", count)
				h.metrics.IncrementRequestCacheCleanUpCount(ctx, count, h.lggr)
			}
		}
	}
}

func (h *connectorHandler) Close() error {
	h.lggr.Debug("Stopping request handler")
	return h.StopOnce(HandlerName, func() error {
		close(h.stopChan)
		h.wg.Wait()
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

func (h *connectorHandler) RegisterWorkflow(ctx context.Context, workflowSelector gateway_common.WorkflowSelector, input *http.Config, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	authorizedKeys, err := h.validateAuthorizedKeys(input.AuthorizedKeys)
	if err != nil {
		return err
	}

	// Push workflow metadata to the gateway
	// Error is non-critical. Retries will be handled by the metadata publisher.
	startTime := time.Now()
	h.metrics.IncrementBroadcastMetadataCount(ctx, h.lggr)
	err = h.gatewayMetadataPublisher.BroadcastWorkflowMetadata(ctx, workflowSelector, authorizedKeys)
	if err != nil {
		h.lggr.Errorw("Failed to push metadata to gateway", "error",
			err, "workflowID", workflowSelector.WorkflowID)
		h.metrics.IncrementBroadcastMetadataFailures(ctx, h.lggr)
	}
	latencyMs := time.Since(startTime).Milliseconds()
	h.metrics.RecordBroadcastMetadataLatency(ctx, latencyMs, h.lggr)

	workflow := newWorkflow(workflowSelector, authorizedKeys, sendCh)
	h.workflowStore.upsertWorkflow(workflow)
	h.lggr.Debugw("Registered workflow", "workflowID", workflowSelector.WorkflowID, "workflowOwner", workflowSelector.WorkflowOwner, "workflowName", workflowSelector.WorkflowName, "workflowTag", workflowSelector.WorkflowTag)
	return nil
}

func (h *connectorHandler) validateAuthorizedKeys(inputKeys []*http.AuthorizedKey) ([]gateway_common.AuthorizedKey, error) {
	if len(inputKeys) == 0 {
		return nil, fmt.Errorf("no authorized keys")
	}
	if len(inputKeys) > int(h.config.MaxAuthorizedKeysPerWorkflow) {
		return nil, fmt.Errorf("too many authorized keys: %d, max allowed: %d", len(inputKeys), h.config.MaxAuthorizedKeysPerWorkflow)
	}

	var authorizedKeys []gateway_common.AuthorizedKey
	for _, key := range inputKeys {
		switch key.Type {
		case http.KeyType_KEY_TYPE_ECDSA_EVM:
			if len(key.PublicKey) != ecdsaPubKeyHexLen || key.PublicKey[:2] != "0x" {
				return nil, fmt.Errorf("invalid public key format: must be 0x-prefixed hex string of length %d, got %q", ecdsaPubKeyHexLen, key.PublicKey)
			}
			authorizedKeys = append(authorizedKeys, gateway_common.AuthorizedKey{
				KeyType:   gateway_common.KeyTypeECDSAEVM,
				PublicKey: strings.ToLower(key.PublicKey),
			})
		default:
			return nil, fmt.Errorf("unsupported key type: %s", key.Type)
		}
	}
	return authorizedKeys, nil
}

func (h *connectorHandler) UnregisterWorkflow(ctx context.Context, workflowID string) error {
	err := h.workflowStore.removeWorkflow(workflowID)
	if err != nil {
		return fmt.Errorf("failed to unregister workflow %s: %w", workflowID, err)
	}
	h.lggr.Debugf("Unregistered workflow %s", workflowID)
	return nil
}

// HandleGatewayMessage processes incoming messages from gateways.
// Always returns nil. Unless request is malformed or rate-limited, response is sent back to the
// gateway using sendResponse method.
func (h *connectorHandler) HandleGatewayMessage(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error {
	if !h.checkIncomingRateLimit(ctx, gatewayID) {
		return nil
	}

	switch req.Method {
	case gateway_common.MethodWorkflowExecute:
		startTime := time.Now()
		h.processTrigger(ctx, gatewayID, req)
		latencyMs := time.Since(startTime).Milliseconds()
		h.metrics.RecordRequestLatency(ctx, latencyMs, h.lggr)
	case gateway_common.MethodPullWorkflowMetadata:
		// No retries here. Retries are orchestrated by the gateway node
		startTime := time.Now()
		h.metrics.IncrementPullMetadataCount(ctx, h.lggr)
		err := h.gatewayMetadataPublisher.SendWorkflowMetadata(ctx, gatewayID, req)
		if err != nil {
			h.lggr.Errorw("Failed to handle pull metadata request", "error",
				err, "gatewayID", gatewayID, "requestID", req.ID)
			h.metrics.IncrementPullMetadataFailures(ctx, h.lggr)
		}
		latencyMs := time.Since(startTime).Milliseconds()
		h.metrics.RecordPullMetadataLatency(ctx, latencyMs, h.lggr)
	default:
		h.lggr.Errorw("Unsupported method", "method", req.Method, "gatewayID", gatewayID)
	}
	return nil
}

func (h *connectorHandler) checkIncomingRateLimit(ctx context.Context, gatewayID string) bool {
	senderAllow, globalAllow := h.incomingRateLimiter.AllowVerbose(gatewayID)
	if !senderAllow {
		h.lggr.Errorw(errorIncomingRatelimitSender, "gatewayID", gatewayID)
		h.metrics.IncrementGatewayNodeThrottled(ctx, gatewayID, h.lggr)
		return false
	}
	if !globalAllow {
		h.lggr.Errorw(errorIncomingRatelimitGlobal, "gatewayID", gatewayID)
		h.metrics.IncrementGatewayGlobalThrottled(ctx, h.lggr)
		return false
	}
	return true
}

func (h *connectorHandler) sendErrorResponse(ctx context.Context, gatewayID string, reqID string, code int64, message string) {
	resp := &jsonrpc.Response[json.RawMessage]{
		Version: "2.0",
		ID:      reqID,
		Method:  gateway_common.MethodWorkflowExecute,
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
	h.metrics.IncrementGatewayRequestCount(ctx, gatewayID, gateway_common.MethodWorkflowExecute, h.lggr)
	err := h.gatewayConnector.SendToGateway(ctx, gatewayID, resp)
	if err != nil {
		h.lggr.Errorw("Failed to send response to gateway", "error", err, "gatewayID", gatewayID)
		h.metrics.IncrementGatewaySendError(ctx, gatewayID, gateway_common.MethodWorkflowExecute, h.lggr)
		return
	}
}

func (h *connectorHandler) processTrigger(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) {
	h.metrics.IncrementRequestCount(ctx, h.lggr)

	if req.Params == nil {
		h.lggr.Errorw("No params in request", "gatewayID", gatewayID, "requestID", req.ID)
		return
	}
	var triggerReq gateway_common.HTTPTriggerRequest
	err := json.Unmarshal(*req.Params, &triggerReq)
	if err != nil {
		h.lggr.Errorw("Failed to unmarshal HTTP trigger request", "error", err, "gatewayID", gatewayID, "requestID", req.ID)
		return
	}

	l := logger.With(h.lggr, "gatewayID", gatewayID, "requestID", req.ID, "method", req.Method)

	workflowID, err := h.resolveWorkflowID(triggerReq.Workflow, l)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInvalidRequest, "Workflow not registered")
		return
	}

	l = logger.With(l, "workflowID", workflowID)
	workflowExecutionID, err := h.generateWorkflowExecutionID(workflowID, req.ID, l)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return
	}

	if handled := h.handleRequestCaching(ctx, gatewayID, req, workflowExecutionID, l); handled {
		return
	}

	resp, err := h.prepareAndCacheResponse(ctx, gatewayID, req, workflowID, workflowExecutionID, l)
	if err != nil {
		return // Error already sent in the method
	}

	// Emit TriggerExecutionStarted event
	if emitErr := EmitTriggerExecutionStarted(ctx, req.ID, workflowExecutionID); emitErr != nil {
		l.Errorw("failed to emit trigger execution started event", "error", emitErr, "workflowID", workflowID, "workflowExecutionID", workflowExecutionID)
		// Continue with execution even if event emission fails
	}

	input := []byte(triggerReq.Input)
	err = h.triggerWorkflow(ctx, workflowID, req.ID, gatewayID, workflowExecutionID, input, triggerReq.Key)
	if err != nil {
		l.Errorw("Failed to trigger workflow", "error", err)
		return
	}

	h.sendResponse(ctx, gatewayID, resp)
}

func (h *connectorHandler) resolveWorkflowID(workflow gateway_common.WorkflowSelector, l logger.Logger) (string, error) {
	workflowID := workflow.WorkflowID
	if workflowID != "" {
		return workflowID, nil
	}

	workflowName := ensureHexPrefix(hex.EncodeToString([]byte(workflows.HashTruncateName(workflow.WorkflowName))))
	resolvedID, exists := h.workflowStore.getWorkflowIDByReference(
		workflow.WorkflowOwner,
		workflowName,
		workflow.WorkflowTag,
	)
	if !exists {
		l.Errorw("Workflow not registered", "workflowOwner", workflow.WorkflowOwner, "workflowName", workflow.WorkflowName, "workflowTag", workflow.WorkflowTag)
		return "", fmt.Errorf("workflow not found")
	}
	return resolvedID, nil
}

func (h *connectorHandler) generateWorkflowExecutionID(workflowID, reqID string, l logger.Logger) (string, error) {
	workflowExecutionID, err := workflows.EncodeExecutionID(strings.TrimPrefix(workflowID, "0x"), reqID)
	if err != nil {
		l.Errorw("Failed to generate workflow execution ID", "error", err)
		return "", err
	}
	return ensureHexPrefix(workflowExecutionID), nil
}

func (h *connectorHandler) handleRequestCaching(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage], workflowExecutionID string, l logger.Logger) bool {
	reqHash, err := req.Digest()
	if err != nil {
		h.lggr.Errorw("Failed to compute request digest", "error", err, "gatewayID", gatewayID, "requestID", req.ID)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return true
	}

	cachedEntry, err := h.requestCache.get(ctx, req.ID)
	if err != nil {
		l.Debugw("cached entry not found. Proceeding with request processing", "error", err)
		return false // not handled, continue processing
	}

	if cachedEntry != nil {
		if cachedEntry.ReqHash == reqHash {
			l.Debugw("Returning cached response for duplicate request", "workflowID", cachedEntry.WorkflowID, "executionID", cachedEntry.ExecutionID)
			h.sendResponse(ctx, gatewayID, cachedEntry.Response)
			return true
		}
		l.Errorw("Request already in progress with different payload", "workflowID", cachedEntry.WorkflowID, "executionID", cachedEntry.ExecutionID)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrConflict, "Request already in progress with different payload")
		return true
	}
	return false // not handled, continue processing
}

func (h *connectorHandler) prepareAndCacheResponse(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage], workflowID, workflowExecutionID string, l logger.Logger) (*jsonrpc.Response[json.RawMessage], error) {
	payload := &gateway_common.HTTPTriggerResponse{
		WorkflowID:          workflowID,
		WorkflowExecutionID: workflowExecutionID,
		Status:              gateway_common.HTTPTriggerStatusAccepted,
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		l.Errorw("Failed to marshal HTTP trigger response", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return nil, err
	}

	payloadMsg := json.RawMessage(jsonPayload)
	resp := &jsonrpc.Response[json.RawMessage]{
		Version: "2.0",
		ID:      req.ID,
		Method:  gateway_common.MethodWorkflowExecute,
		Result:  &payloadMsg,
	}

	reqHash, err := req.Digest()
	if err != nil {
		l.Errorw("Failed to compute request digest for caching", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return nil, err
	}

	err = h.requestCache.add(ctx, requestCacheEntry{
		ReqHash:     reqHash,
		Response:    resp,
		WorkflowID:  workflowID,
		ExecutionID: workflowExecutionID,
		RequestID:   req.ID,
	})
	if err != nil {
		l.Errorw("Failed to add request to cache", "error", err)
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return nil, err
	}

	return resp, nil
}

func (h *connectorHandler) triggerWorkflow(ctx context.Context, workflowID string, reqID string, gatewayID string, executionID string, input []byte, key gateway_common.AuthorizedKey) error {
	workflow, ok := h.workflowStore.getWorkflowByID(workflowID)
	if !ok {
		h.sendErrorResponse(ctx, gatewayID, reqID, jsonrpc.ErrInvalidRequest, "Workflow not registered")
		return fmt.Errorf("workflowID %s not registered", workflowID)
	}
	err := workflow.trigger(ctx, capabilities.TriggerAndId[*http.Payload]{
		// workflow engine does not process the request if the ID has already been used
		Id: executionID,
		Trigger: &http.Payload{
			Input: input,
			Key: &http.AuthorizedKey{
				Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
				PublicKey: key.PublicKey,
			},
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

// EmitTriggerExecutionStarted emits a TriggerExecutionStarted event via beholder
func EmitTriggerExecutionStarted(ctx context.Context, triggerID, workflowExecutionID string) error {
	event := &workflowsevents.TriggerExecutionStarted{
		TriggerID:           triggerID,
		WorkflowExecutionID: workflowExecutionID,
		Timestamp:           time.Now().Format(time.RFC3339),
	}

	// Marshal the protobuf message
	b, err := proto.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal TriggerExecutionStarted event: %w", err)
	}

	// Emit via beholder
	return beholder.GetEmitter().Emit(ctx, b,
		"beholder_data_schema", "workflows.v2.trigger_execution_started", // required
		"beholder_domain", "platform", // required
		"beholder_entity", "workflows.v2.TriggerExecutionStarted") // required
}
