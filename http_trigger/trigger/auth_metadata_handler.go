package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/NethermindEth/juno/jsonrpc"
	"github.com/cockroachdb/errors"
	"github.com/google/uuid"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	// TODO: move this to chainlink-common
	MethodWorkflowPushAuthMetadata = "workflow.push_auth_metadata"
	MethodWorkflowPullAuthMetadata = "workflow.pull_auth_metadata"
)

// TODO: move this to chainlink-common
type WorkflowAuthMetadata struct {
	WorkflowID     string
	AuthorizedKeys []AuthorizedKey
}

type KeyType string

const (
	KeyTypeECDSA KeyType = "ecdsa"
)

type AuthorizedKey struct {
	KeyType   KeyType
	PublicKey string
}

type authMetadataHandler struct {
	lggr                logger.Logger
	gc                  core.GatewayConnector
	outgoingRateLimiter *ratelimit.RateLimiter
	workflowFetcher     WorkflowFetcher
	cfg                 ServiceConfig
}

type WorkflowFetcher interface {
	GetWorkflows() ([]*workflow, error)
}

func NewAuthMetadataHandler(
	lggr logger.Logger,
	gc core.GatewayConnector,
	outgoingRateLimiter *ratelimit.RateLimiter,
	workflowFetcher WorkflowFetcher,
) *authMetadataHandler {
	return &authMetadataHandler{
		lggr:                lggr,
		gc:                  gc,
		outgoingRateLimiter: outgoingRateLimiter,
		workflowFetcher:     workflowFetcher,
	}
}

func (h *authMetadataHandler) PushToGateway(ctx context.Context, workflowID string, keys []AuthorizedKey) error {
	authData := WorkflowAuthMetadata{
		WorkflowID:     workflowID,
		AuthorizedKeys: keys,
	}
	payload, err := json.Marshal(authData)
	if err != nil {
		return errors.New("failed to marshal auth metadata")
	}
	gatewayResp := jsonrpc.Response{
		Version: "2.0",
		ID:      fmt.Sprintf("%s/%s/%s", MethodWorkflowPushAuthMetadata, workflowID, uuid.New().String()),
		Result:  json.RawMessage(payload),
	}
	gatewayIDs, err := h.gc.GatewayIDs(ctx)
	if err != nil {
		return fmt.Errorf("failed to get gateway IDs: %w", err)
	}
	for _, gatewayID := range gatewayIDs {
		// TODO:
		// attempt connection
		// retry with backoff until timeout
		// send
		// workflowAllow, globalAllow := p.outgoingRateLimiter.AllowVerbose(metadata.WorkflowOwner)
		// 	if !workflowAllow {
		// 		return errors.New(common.ErrorOutgoingRatelimitWorkflowOwner)
		// 	}
		// 	if !globalAllow {
		// 		return errors.New(common.ErrorOutgoingRatelimitGlobal)
		// 	}
		err := h.gc.SendToGateway(ctx, gatewayID, &gatewayResp)
		if err != nil {
			h.lggr.Errorw("failed to send auth metadata to gateway", "gatewayID", gatewayID, "error", err)
			continue // try next gateway
		}
	}
	return nil
}

func (h *authMetadataHandler) HandlePullFromGateway(ctx context.Context) error {
	workflows, err := h.workflowFetcher.GetWorkflows()
	if err != nil {
		h.lggr.Errorw("failed to fetch workflows", "error", err)
		return
	}
	batchSize := int(h.cfg.AuthMetdataBatchSize)
	for i := 0; i < len(workflows); i += batchSize {
		end := i + batchSize
		if end > len(workflows) {
			end = len(workflows)
		}
		batch := workflows[i:end]

		var batchAuthData []WorkflowAuthMetadata
		for _, wf := range batch {
			for key := range wf.authorizedKeys {
			batchAuthData = append(batchAuthData, WorkflowAuthMetadata{
				WorkflowID:     wf.workflowID,
				AuthorizedKeys: wf.authorizedKeys,
			})
		}

		payload, err := json.Marshal(batchAuthData)
		if err != nil {
			h.lggr.Errorw("failed to marshal batch auth metadata", "error", err)
			continue
		}

		gatewayResp := jsonrpc.Response{
			Version: "2.0",
			ID:      fmt.Sprintf("%s/%s", MethodWorkflowPullAuthMetadata + uuid.New().String()),
			Result:  json.RawMessage(payload),
		}

		gatewayIDs, err := h.gc.GatewayIDs(ctx)
		if err != nil {
			h.lggr.Errorw("failed to get gateway IDs", "error", err)
			return
		}

		for _, gatewayID := range gatewayIDs {
			err := h.gc.SendToGateway(ctx, gatewayID, &gatewayResp)
			if err != nil {
				
			}
		}
	}
}

// 	selectedGateway, err := p.awaitConnection(ctx, lggr)
// 	if err != nil {
// 		return nil, errors.Join(errors.New("failed to await connection to gateway"), err)
// 	}

// 	if err := p.gatewayConnector.SendToGateway(ctx, selectedGateway, &gatewayResp); err != nil {
// 		return nil, errors.Join(errors.New("failed to send request to gateway"), err)
// 	}

// TODO: consolidate this with HTTP action
// func attemptGatewayConnection(ctx context.Context, lggr logger.Logger, gateway string, timeout time.Duration) error {
// 	lggr.Debugw("awaiting connection", "timeout", timeout)

// 	// create a new child context to wait on gateway connection
// 	ctxWithTimeout, cancel := context.WithTimeout(ctx, timeout)
// 	defer cancel()

// 	if err := p.gatewayConnector.AwaitConnection(ctxWithTimeout, gateway); err != nil {
// 		return fmt.Errorf("gateway connection failed: %w", err)
// 	}
// 	return nil
// }
