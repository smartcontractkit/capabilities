package capcommon

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
)

type stubOrgResolver struct {
	orgID string
	err   error
}

func (s stubOrgResolver) Get(_ context.Context, _ string) (string, error) { return s.orgID, s.err }
func (s stubOrgResolver) Start(_ context.Context) error                   { return nil }
func (s stubOrgResolver) Close() error                                    { return nil }
func (s stubOrgResolver) HealthReport() map[string]error                  { return nil }
func (s stubOrgResolver) Ready() error                                    { return nil }
func (s stubOrgResolver) Name() string                                    { return "stubOrgResolver" }

var _ orgresolver.OrgResolver = stubOrgResolver{}

func TestContextWithOrgForDelivery(t *testing.T) {
	t.Parallel()

	owner := "0xOwner"
	meta := capabilities.RequestMetadata{WorkflowOwner: owner, WorkflowID: "wf-1"}

	t.Run("preserves org already on ctx", func(t *testing.T) {
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Org: "existing-org"})
		got := ContextWithOrgForDelivery(ctx, logger.Test(t), nil, meta)
		require.Equal(t, "existing-org", contexts.CREValue(got).Org)
	})

	t.Run("uses OrgID from registration metadata", func(t *testing.T) {
		md := capabilities.RequestMetadata{WorkflowOwner: owner, WorkflowID: "wf-1", OrgID: "meta-org"}
		got := ContextWithOrgForDelivery(t.Context(), logger.Test(t), nil, md)
		require.Equal(t, "meta-org", contexts.CREValue(got).Org)
		require.Equal(t, "owner", contexts.CREValue(got).Owner)
	})

	t.Run("resolves org via orgResolver", func(t *testing.T) {
		resolver := stubOrgResolver{orgID: "resolved-org"}
		got := ContextWithOrgForDelivery(t.Context(), logger.Test(t), resolver, meta)
		require.Equal(t, "resolved-org", contexts.CREValue(got).Org)
		require.Equal(t, "owner", contexts.CREValue(got).Owner)
		require.Equal(t, "wf-1", contexts.CREValue(got).Workflow)
	})

	t.Run("no org when resolver returns empty", func(t *testing.T) {
		resolver := stubOrgResolver{}
		got := ContextWithOrgForDelivery(t.Context(), logger.Test(t), resolver, meta)
		require.Empty(t, contexts.CREValue(got).Org)
	})

	t.Run("no org when orgResolver is nil", func(t *testing.T) {
		got := ContextWithOrgForDelivery(t.Context(), logger.Test(t), nil, meta)
		require.Empty(t, contexts.CREValue(got).Org)
	})
}
