package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

type authMetadataHandler struct {
	lggr                logger.Logger
	gc                  core.GatewayConnector
	outgoingRateLimiter *ratelimit.RateLimiter
	workflowStore       WorkflowStore
	cfg                 ServiceConfig
}

func NewAuthMetadataHandler(
	lggr logger.Logger,
	gc core.GatewayConnector,
	outgoingRateLimiter *ratelimit.RateLimiter,
	workflowStore WorkflowStore,
) *authMetadataHandler {
	return &authMetadataHandler{
		lggr:                lggr,
		gc:                  gc,
		outgoingRateLimiter: outgoingRateLimiter,
		workflowStore:       workflowStore,
	}
}

type AuthMetadataHandler interface {
	// SendWorkflows sends all workflows' authentication metadata to the gateway in batches
	// It is expected to send a response back to the gateway using the gatewayConnector then return error
	SendWorkflows(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error
	// BroadcastWorkflow sends the authentication metadata to the gateway.
	BroadcastWorkflow(ctx context.Context, workflowID string, keys []gateway.AuthorizedKey) error
}

func (h *authMetadataHandler) BroadcastWorkflow(ctx context.Context, workflowID string, keys []gateway.AuthorizedKey) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.cfg.GatewayConnectionConfig.MaxPushAuthMetadataDurationMs))
	defer cancel()
	authData := gateway.WorkflowAuthMetadata{
		WorkflowID:     workflowID,
		AuthorizedKeys: keys,
	}
	payload, err := json.Marshal(authData)
	if err != nil {
		return errors.New("failed to marshal auth metadata")
	}
	rawRes := json.RawMessage(payload)
	gatewayResp := jsonrpc.Response[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      gateway.GetRequestID(gateway.MethodWorkflowPushAuthMetadata, workflowID),
		Result:  &rawRes,
	}
	gatewayIDs, err := h.gc.GatewayIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get gateway IDs: %w", err)
	}
	for _, gatewayID := range gatewayIDs {
		backoff := time.Duration(h.cfg.GatewayConnectionConfig.RetryConfig.InitialIntervalMs) * time.Millisecond
		for {
			err := h.sendResponse(ctx, gatewayID, &gatewayResp)
			if err == nil {
				continue
			}
			h.lggr.Debugw("failed to send auth metadata to gateway. Retrying", "gatewayID", gatewayID, "error", err)
			select {
			case <-ctx.Done():
				return fmt.Errorf("context canceled while awaiting connection to gateway %s: %w", gatewayID, ctx.Err())
			case <-time.After(backoff):
				backoff = nextBackoff(backoff,
					h.cfg.GatewayConnectionConfig.RetryConfig.Multiplier,
					time.Duration(h.cfg.GatewayConnectionConfig.MaxPushAuthMetadataDurationMs)*time.Millisecond)
				continue
			}
		}
	}
	return nil
}

func (h *authMetadataHandler) sendErrorResponse(ctx context.Context, gatewayID string, reqID string, code int64, message string) {
	resp := &jsonrpc.Response[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      reqID,
		Error: &jsonrpc.WireError{
			Code:    code,
			Message: message,
		},
	}
	err := h.sendResponse(ctx, gatewayID, resp)
	if err != nil {
		h.lggr.Errorw("failed to send error response to gateway", "gatewayID", gatewayID, "error", err)
	}
}

func (h *authMetadataHandler) SendWorkflows(ctx context.Context, gatewayID string, req *jsonrpc.Request[json.RawMessage]) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.cfg.GatewayConnectionConfig.MaxPullAuthMetadataDurationMs))
	defer cancel()
	workflows, err := h.workflowStore.GetWorkflows()
	if err != nil {
		h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "failed to fetch workflows")
		return fmt.Errorf("failed to fetch workflows: %w", err)
	}
	batchSize := int(h.cfg.AuthMetdataBatchSize)
	for i := 0; i < len(workflows); i += batchSize {
		end := i + batchSize
		if end > len(workflows) {
			end = len(workflows)
		}
		batch := workflows[i:end]

		var batchAuthData []gateway.WorkflowAuthMetadata
		for _, wf := range batch {
			var keys []gateway.AuthorizedKey
			for _, key := range wf.authorizedKeys {
				keys = append(keys, key)
			}
			batchAuthData = append(batchAuthData, gateway.WorkflowAuthMetadata{
				WorkflowID:     wf.workflowID,
				AuthorizedKeys: keys,
			})
		}

		payload, err := json.Marshal(batchAuthData)
		if err != nil {
			h.sendErrorResponse(ctx, gatewayID, req.ID, jsonrpc.ErrInternal, "failed to marshal batch auth metadata")
			return fmt.Errorf("failed to marshal batch auth metadata: %w", err)
		}

		rawRes := json.RawMessage(payload)
		gatewayResp := jsonrpc.Response[json.RawMessage]{
			Version: jsonrpc.JsonRpcVersion,
			ID:      gateway.GetRequestID(gateway.MethodWorkflowPullAuthMetadata, req.ID),
			Result:  &rawRes,
		}

		err = h.sendResponse(ctx, gatewayID, &gatewayResp)
		if err != nil {
			return fmt.Errorf("failed to send batch auth metadata to gateway for workflow: %w", err)
		}
	}
	return nil
}

func (h *authMetadataHandler) sendResponse(ctx context.Context, gatewayID string, resp *jsonrpc.Response[json.RawMessage]) error {
	workflowAllow, globalAllow := h.outgoingRateLimiter.AllowVerbose(gatewayID)
	if !workflowAllow {
		return errors.New(errorOutgoingRatelimitSender)
	}
	if !globalAllow {
		return errors.New(errorOutgoingRatelimitGlobal)
	}
	err := h.gc.SendToGateway(ctx, gatewayID, resp)
	if err != nil {
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
