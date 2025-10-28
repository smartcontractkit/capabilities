package trigger

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/events"
)

const (
	HandlerName       = "HTTPTriggerHandler"
	ecdsaPubKeyHexLen = 42 // 2 (0x prefix) + 40 (hex digits)
)

var _ core.GatewayConnectorHandler = &connectorHandler{}

type connectorHandler struct {
	services.StateMachine
	lggr                     logger.Logger
	gatewayConnector         core.GatewayConnector
	config                   ServiceConfig
	requestCache             *requestCache
	workflowStore            *workflowStore
	gatewayMetadataPublisher GatewayMetadataPublisher
	metrics                  *Metrics
	wg                       sync.WaitGroup
	stopChan                 services.StopChan
	orgResolver              orgresolver.OrgResolver // Optional org resolver for fetching organization IDs
}

func NewConnectorHandler(lggr logger.Logger, gc core.GatewayConnector, config ServiceConfig,
	workflowStore *workflowStore, gatewayMetadataPublisher GatewayMetadataPublisher, requestCache *requestCache, metrics *Metrics, orgResolver orgresolver.OrgResolver) (*connectorHandler, error) {
	return &connectorHandler{
		lggr:                     logger.Named(lggr, HandlerName),
		gatewayConnector:         gc,
		config:                   config,
		workflowStore:            workflowStore,
		gatewayMetadataPublisher: gatewayMetadataPublisher,
		requestCache:             requestCache,
		metrics:                  metrics,
		stopChan:                 make(chan struct{}),
		orgResolver:              orgResolver,
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
	return h.lggr.Name()
}

func (h *connectorHandler) ID(context.Context) (string, error) {
	return HandlerName, nil
}

func (h *connectorHandler) RegisterWorkflow(ctx context.Context, input WorkflowRegistrationInput, sendCh chan<- capabilities.TriggerAndId[*http.Payload]) error {
	if input.Config == nil {
		return errors.New("input config cannot be nil")
	}
	authorizedKeys, err := h.validateAuthorizedKeys(input.Config.AuthorizedKeys)
	if err != nil {
		return err
	}

	// Push workflow metadata to the gateway
	// Error is non-critical. Retries will be handled by the metadata publisher.
	startTime := time.Now()
	h.metrics.IncrementBroadcastMetadataCount(ctx, h.lggr)
	err = h.gatewayMetadataPublisher.BroadcastWorkflowMetadata(ctx, input.WorkflowSelector, authorizedKeys)
	if err != nil {
		h.lggr.Errorw("Failed to push metadata to gateway", "error",
			err, "workflowID", input.WorkflowSelector.WorkflowID)
		h.metrics.IncrementBroadcastMetadataFailures(ctx, h.lggr)
	}
	latencyMs := time.Since(startTime).Milliseconds()
	h.metrics.RecordBroadcastMetadataLatency(ctx, latencyMs, h.lggr)

	workflow := newWorkflowWithMetadata(input.WorkflowSelector, authorizedKeys, sendCh, input.Metadata)
	if err := h.workflowStore.upsertWorkflow(workflow); err != nil {
		return fmt.Errorf("failed to register workflow (ID: %s, Owner: %s, Name: %s): %w",
			input.WorkflowSelector.WorkflowID, input.WorkflowSelector.WorkflowOwner, input.WorkflowSelector.WorkflowName, err)
	}
	h.lggr.Debugw("Registered workflow", "workflowID", input.WorkflowSelector.WorkflowID, "workflowOwner", input.WorkflowSelector.WorkflowOwner, "workflowName", input.WorkflowSelector.WorkflowName, "workflowTag", input.WorkflowSelector.WorkflowTag)
	return nil
}

func (h *connectorHandler) validateAuthorizedKeys(inputKeys []*http.AuthorizedKey) ([]gateway_common.AuthorizedKey, error) {
	if len(inputKeys) == 0 {
		return nil, fmt.Errorf("HTTP trigger requires at least one authorized key to sign JSON-RPC requests. Add AuthorizedKeys to your http.Trigger configuration with ECDSA EVM public keys (0x-prefixed hex strings)")
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
	if req == nil {
		return errors.New("request cannot be nil")
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
	h.metrics.IncrementGatewayRequestCount(ctx, gatewayID, gateway_common.MethodWorkflowExecute, h.lggr)
	err := h.gatewayConnector.SendToGateway(ctx, gatewayID, resp)
	if err != nil {
		h.lggr.Errorw("Failed to send response to gateway", "error", err, "gatewayID", gatewayID, "requestID", resp.ID)
		h.metrics.IncrementGatewaySendError(ctx, gatewayID, gateway_common.MethodWorkflowExecute, h.lggr)
		return
	}
}

func (h *connectorHandler) processTrigger(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) {
	h.metrics.IncrementRequestCount(ctx, h.lggr)

	if req == nil {
		h.lggr.Errorw("Request cannot be nil", "gatewayID", gatewayID)
		return
	}

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

	workflowMetadata, err := h.resolveWorkflowMetadata(triggerReq.Workflow, l)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInvalidRequest, "Workflow not registered")
		return
	}

	l = logger.With(l, "workflowID", workflowMetadata.WorkflowID)
	workflowExecutionID, err := h.generateWorkflowExecutionID(strings.TrimPrefix(workflowMetadata.WorkflowID, "0x"), req.ID, l)
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "Internal server error")
		return
	}

	l = logger.With(l, "workflowExecutionID", workflowExecutionID)
	if handled := h.handleRequestCaching(ctx, gatewayID, req, workflowExecutionID, l); handled {
		return
	}

	resp, err := h.prepareAndCacheResponse(ctx, gatewayID, req, workflowMetadata.WorkflowID, workflowExecutionID, l)
	if err != nil {
		return // Error already sent in the method
	}

	// Emit TriggerExecutionStarted event
	labeler := custmsg.NewLabeler().With(
		events.KeyTriggerID, req.ID,
		events.KeyWorkflowID, workflowMetadata.WorkflowID,
		events.KeyWorkflowExecutionID, workflowExecutionID,
		events.KeyWorkflowOwner, workflowMetadata.WorkflowOwner,
		events.KeyWorkflowName, workflowMetadata.WorkflowName,
		events.KeyWorkflowRegistryChainSelector, workflowMetadata.WorkflowRegistryChainSelector,
		events.KeyWorkflowRegistryAddress, workflowMetadata.WorkflowRegistryAddress,
		events.KeyEngineVersion, workflowMetadata.EngineVersion,
		events.KeyDonID, strconv.Itoa(int(workflowMetadata.WorkflowDONID)),
	)

	// Try to fetch organization ID if org resolver is available
	if h.orgResolver != nil && workflowMetadata.WorkflowOwner != "" {
		if orgID, orgErr := h.orgResolver.Get(ctx, workflowMetadata.WorkflowOwner); orgErr != nil {
			l.Warnw("Failed to fetch organization ID from org resolver", "workflowOwner", workflowMetadata.WorkflowOwner, "error", orgErr)
		} else if orgID != "" {
			labeler = labeler.With(events.KeyOrganizationID, orgID)
			l.Debugw("Successfully fetched organization ID", "workflowOwner", workflowMetadata.WorkflowOwner, "orgID", orgID)
		}
	}

	if emitErr := events.EmitTriggerExecutionStarted(ctx, labeler); emitErr != nil {
		l.Errorw("failed to emit trigger execution started event", "error", emitErr)
		// Continue with execution even if event emission fails
	}

	l.Debugw("Triggering workflow")
	input := []byte(triggerReq.Input)
	err = h.triggerWorkflow(ctx, workflowMetadata.WorkflowID, req.ID, gatewayID, input, triggerReq.Key)
	if err != nil {
		l.Errorw("Failed to trigger workflow", "error", err)
		return
	}

	h.sendResponse(ctx, gatewayID, resp)
}

type WorkflowMetadata struct {
	WorkflowID                    string
	WorkflowOwner                 string
	WorkflowName                  string
	WorkflowTag                   string
	WorkflowRegistryChainSelector string
	WorkflowRegistryAddress       string
	EngineVersion                 string
	WorkflowDONID                 uint32
}

func (h *connectorHandler) resolveWorkflowMetadata(workflow gateway_common.WorkflowSelector, l logger.Logger) (WorkflowMetadata, error) {
	// Normalize workflowID and workflowOwner before any operations
	normalizedWorkflowID := normalizeHex(workflow.WorkflowID, expectedWorkflowIDLen)
	normalizedWorkflowOwner := normalizeHex(workflow.WorkflowOwner, expectedWorkflowOwnerLen)
	hashedWorkflowName := ensureHexPrefix(hex.EncodeToString([]byte(workflows.HashTruncateName(workflow.WorkflowName))))

	metadata := WorkflowMetadata{
		WorkflowID:    normalizedWorkflowID,
		WorkflowOwner: normalizedWorkflowOwner,
		WorkflowName:  workflow.WorkflowName,
		WorkflowTag:   workflow.WorkflowTag,
	}

	if workflow.WorkflowID != "" {
		// Get the workflow from store to access metadata
		h.populateMetadataFromWorkflow(normalizedWorkflowID, &metadata, l)
		return metadata, nil
	}

	resolvedID, exists := h.workflowStore.getWorkflowIDByReference(
		normalizedWorkflowOwner,
		hashedWorkflowName,
		workflow.WorkflowTag,
	)
	if !exists {
		l.Errorw("Workflow not registered", "workflowOwner", normalizedWorkflowOwner, "workflowName", hashedWorkflowName, "workflowTag", workflow.WorkflowTag)
		return WorkflowMetadata{}, fmt.Errorf("workflow not found")
	}

	metadata.WorkflowID = resolvedID
	// Get the workflow from store to access metadata
	h.populateMetadataFromWorkflow(resolvedID, &metadata, l)
	return metadata, nil
}

// populateMetadataFromWorkflow retrieves metadata from the workflow store and populates the WorkflowMetadata struct
func (h *connectorHandler) populateMetadataFromWorkflow(workflowID string, metadata *WorkflowMetadata, l logger.Logger) {
	if w, exists := h.workflowStore.getWorkflowByID(workflowID); exists {
		metadata.WorkflowRegistryChainSelector = w.metadata.WorkflowRegistryChainSelector
		metadata.WorkflowRegistryAddress = w.metadata.WorkflowRegistryAddress
		metadata.EngineVersion = w.metadata.EngineVersion
		metadata.WorkflowDONID = w.metadata.WorkflowDONID
		l.Debugw("Retrieved workflow metadata",
			"workflowID", workflowID,
			"registryChainSelector", metadata.WorkflowRegistryChainSelector,
			"registryAddress", metadata.WorkflowRegistryAddress,
			"engineVersion", metadata.EngineVersion,
			"donID", metadata.WorkflowDONID)
	} else {
		l.Warnw("Workflow not found in store", "workflowID", workflowID)
	}
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

func (h *connectorHandler) triggerWorkflow(ctx context.Context, workflowID string, reqID string, gatewayID string, input []byte, key gateway_common.AuthorizedKey) error {
	workflow, ok := h.workflowStore.getWorkflowByID(workflowID)
	if !ok {
		h.sendErrorResponse(ctx, gatewayID, reqID, jsonrpc.ErrInvalidRequest, "Workflow not registered")
		return fmt.Errorf("workflowID %s not registered", workflowID)
	}
	err := workflow.trigger(ctx, capabilities.TriggerAndId[*http.Payload]{
		// workflow engine does not process the request if the ID has already been used
		Id: reqID,
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
