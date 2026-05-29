package trigger

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
)

// contextWithOrgForDelivery returns ctx with CRE org populated when needed for
// BaseTrigger DeliverEvent org-scoped retransmit policy. Registration-time ctx may
// lack org when the workflow DON did not propagate OrgID over P2P; the capability
// DON resolves org locally via orgResolver from WorkflowOwner.
func (lts *LogTriggerService) contextWithOrgForDelivery(ctx context.Context, meta capabilities.RequestMetadata) context.Context {
	if contexts.CREValue(ctx).Org != "" {
		return ctx
	}
	if meta.OrgID != "" {
		return meta.ContextWithCRE(ctx)
	}
	if lts.orgResolver == nil || meta.WorkflowOwner == "" {
		return ctx
	}
	orgID, orgErr := lts.orgResolver.Get(ctx, meta.WorkflowOwner)
	if orgErr != nil {
		lts.lggr.Warnw("failed to resolve organization ID for deliver-time retransmit policy",
			"workflowOwner", meta.WorkflowOwner, "err", orgErr)
		return ctx
	}
	if orgID == "" {
		return ctx
	}
	meta.OrgID = orgID
	return meta.ContextWithCRE(ctx)
}
