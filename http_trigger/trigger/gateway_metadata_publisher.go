package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

var _ GatewayMetadataPublisher = (*gatewayMetadataPublisher)(nil)

type gatewayMetadataPublisher struct {
	lggr          logger.Logger
	gc            core.GatewayConnector
	workflowStore *workflowStore
	cfg           ServiceConfig
	metrics       *Metrics
}

func NewGatewayMetadataPublisher(
	lggr logger.Logger,
	gc core.GatewayConnector,
	workflowStore *workflowStore,
	cfg ServiceConfig,
	metrics *Metrics,
) *gatewayMetadataPublisher {
	return &gatewayMetadataPublisher{
		lggr:          lggr,
		gc:            gc,
		workflowStore: workflowStore,
		cfg:           cfg,
		metrics:       metrics,
	}
}

type GatewayMetadataPublisher interface {
	// SendWorkflowMetadata sends all workflows' metadata to the gateway in batches
	// It is expected to send a response back to the gateway using the gatewayConnector then return error
	SendWorkflowMetadata(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error
	// BroadcastWorkflowMetadata sends the metadata to all gateways.
	BroadcastWorkflowMetadata(ctx context.Context, workflowSelector gateway.WorkflowSelector, keys []gateway.AuthorizedKey) error
}

func (h *gatewayMetadataPublisher) BroadcastWorkflowMetadata(ctx context.Context, workflowSelector gateway.WorkflowSelector, keys []gateway.AuthorizedKey) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.cfg.GatewayConnectionConfig.MaxPushMetadataDurationMs)*time.Millisecond)
	defer cancel()
	metadata := gateway.WorkflowMetadata{
		WorkflowSelector: workflowSelector,
		AuthorizedKeys:   keys,
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}
	rawRes := json.RawMessage(payload)
	gatewayResp := jsonrpc.Response[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      gateway.GetRequestID(gateway.MethodPushWorkflowMetadata, workflowSelector.WorkflowID),
		Method:  gateway.MethodPushWorkflowMetadata,
		Result:  &rawRes,
	}
	gatewayIDs, err := h.gc.GatewayIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get gateway IDs: %w", err)
	}
	for _, gatewayID := range gatewayIDs {
		backoff := time.Duration(h.cfg.GatewayConnectionConfig.RetryConfig.InitialIntervalMs) * time.Millisecond
		for {
			err := h.sendResponse(ctx, gatewayID, &gatewayResp, gateway_common.MethodPushWorkflowMetadata)
			if err == nil {
				h.lggr.Debugw("successfully sent metadata", "gatewayID", gatewayID)
				break
			}
			h.lggr.Debugw("failed to send metadata to gateway. Retrying", "gatewayID", gatewayID, "error", err)
			select {
			case <-ctx.Done():
				return fmt.Errorf("context canceled while awaiting connection to gateway %s: %w", gatewayID, ctx.Err())
			case <-time.After(backoff):
				backoff = nextBackoff(backoff,
					h.cfg.GatewayConnectionConfig.RetryConfig.Multiplier,
					time.Duration(h.cfg.GatewayConnectionConfig.RetryConfig.MaxIntervalTimeMs)*time.Millisecond)
				continue
			}
		}
	}
	return nil
}

func (h *gatewayMetadataPublisher) sendErrorResponse(ctx context.Context, gatewayID string, reqID string, code int64, message string, methodName string) {
	resp := &jsonrpc.Response[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      reqID,
		Method:  gateway.MethodPullWorkflowMetadata,
		Error: &jsonrpc.WireError{
			Code:    code,
			Message: message,
		},
	}
	err := h.sendResponse(ctx, gatewayID, resp, methodName)
	if err != nil {
		h.lggr.Errorw("failed to send error response to gateway", "gatewayID", gatewayID, "error", err)
	}
}

func (h *gatewayMetadataPublisher) SendWorkflowMetadata(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error {
	if req == nil {
		return errors.New("request cannot be nil")
	}
	if req.ID == "" {
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInvalidRequest, "empty request ID", gateway_common.MethodPullWorkflowMetadata)
		return errors.New("empty request ID")
	}
	methodName := strings.Split(req.ID, "/")[0]
	if methodName != gateway.MethodPullWorkflowMetadata {
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInvalidRequest, "invalid request ID for workflow pull metadata", gateway_common.MethodPullWorkflowMetadata)
		return fmt.Errorf("invalid request ID for workflow pull metadata: %s", req.ID)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.cfg.GatewayConnectionConfig.MaxPullMetadataDurationMs)*time.Millisecond)
	defer cancel()
	workflows := h.workflowStore.getWorkflows()
	if len(workflows) == 0 {
		h.lggr.Debugw("no workflows found", "gatewayID", gatewayID, "requestID", req.ID)
		return nil
	}
	batchSize := int(h.cfg.MetadataBatchSize)
	for i := 0; i < len(workflows); i += batchSize {
		end := min(i+batchSize, len(workflows))
		batch := workflows[i:end]

		batchData := make([]gateway.WorkflowMetadata, 0, len(batch))
		for _, wf := range batch {
			var keys []gateway.AuthorizedKey
			for key := range wf.authorizedKeys {
				keys = append(keys, key)
			}
			batchData = append(batchData, gateway.WorkflowMetadata{
				WorkflowSelector: wf.workflowSelector,
				AuthorizedKeys:   keys,
			})
		}

		payload, err := json.Marshal(batchData)
		if err != nil {
			h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "failed to marshal batch metadata", gateway_common.MethodPullWorkflowMetadata)
			return fmt.Errorf("failed to marshal batch metadata: %w", err)
		}

		rawRes := json.RawMessage(payload)
		gatewayResp := jsonrpc.Response[json.RawMessage]{
			Version: jsonrpc.JsonRpcVersion,
			ID:      req.ID,
			Method:  gateway.MethodPullWorkflowMetadata,
			Result:  &rawRes,
		}

		err = h.sendResponse(ctx, gatewayID, &gatewayResp, gateway_common.MethodPullWorkflowMetadata)
		if err != nil {
			return fmt.Errorf("failed to send batch metadata to gateway for workflow: %w", err)
		}
	}
	return nil
}

func (h *gatewayMetadataPublisher) sendResponse(ctx context.Context, gatewayID string, resp *jsonrpc.Response[json.RawMessage], methodName string) error {
	h.metrics.IncrementGatewayRequestCount(ctx, gatewayID, methodName, h.lggr)
	err := h.gc.SendToGateway(ctx, gatewayID, resp)
	if err != nil {
		h.metrics.IncrementGatewaySendError(ctx, gatewayID, methodName, h.lggr)
		return fmt.Errorf("failed to send response to gateway %s: %w", gatewayID, err)
	}
	return nil
}

// nextBackoff calculates the next backoff duration using the configured multiplier and max elapsed time.
func nextBackoff(backoff time.Duration, multiplier float64, maxDuration time.Duration) time.Duration {
	backoffMs := float64(backoff.Milliseconds())
	backoffMs = backoffMs * multiplier
	backoffMs = math.Min(backoffMs, float64(maxDuration.Milliseconds()))
	return time.Duration(backoffMs) * time.Millisecond
}
