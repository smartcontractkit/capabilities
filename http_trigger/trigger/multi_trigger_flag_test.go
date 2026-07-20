package trigger

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
)

// TestFeatureMultiTriggerFlagCheckRequiresCRE shows the raw RangeLimiter contract:
// ScopeWorkflow Check fails without CRE on ctx, and succeeds with it.
func TestFeatureMultiTriggerFlagCheckRequiresCRE(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	period := cresettings.Default.PerWorkflow.FeatureMultiTriggerExecutionIDsActivePeriod
	period.DefaultValue = settings.Range[config.Timestamp]{
		Lower: config.Timestamp(now.Add(-time.Hour).Unix()),
		Upper: config.Timestamp(now.Add(time.Hour).Unix()),
	}

	multiTriggerFlag, err := limits.MakeRangeLimiter(limits.Factory{}, period)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, multiTriggerFlag.Close()) })

	strippedWorkflowID := strings.TrimPrefix(testWorkflowID, "0x")
	reqID := "req-multi-trigger-ctx"
	ts := config.NewTimestamp(now)

	legacyID, err := workflows.EncodeExecutionID(strippedWorkflowID, reqID) //nolint:staticcheck // intentional legacy path
	require.NoError(t, err)

	multiID, err := workflows.GenerateExecutionIDWithTriggerIndex(strippedWorkflowID, reqID, 0)
	require.NoError(t, err)

	require.NotEqual(t, legacyID, multiID, "legacy and multi-trigger hashes must differ even for trigger index 0")

	selectExecutionID := func(flagOn bool) string {
		if flagOn {
			return multiID
		}
		return legacyID
	}

	// without_WithCRE_Check_fails_and_selects_legacy_ID: bare ctx → missing tenant → flag treated as off.
	t.Run("without_WithCRE_Check_fails_and_selects_legacy_ID", func(t *testing.T) {
		t.Parallel()

		checkErr := multiTriggerFlag.Check(t.Context(), ts)
		require.Error(t, checkErr)
		assert.ErrorContains(t, checkErr, "unable to get scoped bounds limit due to missing tenant for scope: workflow")

		flagOn := checkErr == nil
		assert.False(t, flagOn)
		assert.Equal(t, legacyID, selectExecutionID(flagOn))
	})

	// with_WithCRE_Check_succeeds_and_selects_multi_trigger_ID: Workflow on ctx → Check passes.
	t.Run("with_WithCRE_Check_succeeds_and_selects_multi_trigger_ID", func(t *testing.T) {
		t.Parallel()

		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: testWorkflowID})
		require.NoError(t, multiTriggerFlag.Check(ctx, ts))

		flagOn := multiTriggerFlag.Check(ctx, ts) == nil
		assert.True(t, flagOn)
		assert.Equal(t, multiID, selectExecutionID(flagOn))
	})
}

// TestGenerateWorkflowExecutionID_WithCREUsesMultiTriggerIDs checks that
// generateWorkflowExecutionID itself attaches CRE, so a bare gateway ctx still
// yields multi-trigger IDs when the flag period is active.
func TestGenerateWorkflowExecutionID_WithCREUsesMultiTriggerIDs(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)

	now := time.Now().UTC()
	period := cresettings.Default.PerWorkflow.FeatureMultiTriggerExecutionIDsActivePeriod
	period.DefaultValue = settings.Range[config.Timestamp]{
		Lower: config.Timestamp(now.Add(-time.Hour).Unix()),
		Upper: config.Timestamp(now.Add(time.Hour).Unix()),
	}
	activeFlag, err := limits.MakeRangeLimiter(limits.Factory{}, period)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, activeFlag.Close()) })
	handler.multiTriggerFlag = activeFlag

	reqID := "req-generate-with-cre"
	refID := "trigger_0"
	wantMulti, err := workflows.GenerateExecutionIDWithTriggerIndex(strings.TrimPrefix(testWorkflowID, "0x"), reqID, 0)
	require.NoError(t, err)
	wantMulti = ensureHexPrefix(wantMulti)

	// Intentionally pass t.Context() without WithCRE — the method under test must add it.
	got, isLegacy, err := handler.generateWorkflowExecutionID(
		t.Context(),
		testWorkflowID,
		testWorkflowOwner,
		"test-org",
		reqID,
		refID,
		lggr,
	)
	require.NoError(t, err)
	assert.False(t, isLegacy)
	assert.Equal(t, wantMulti, got)
}

// TestGenerateWorkflowExecutionID_InactiveFlagUsesLegacyIDs checks that when the
// flag period does not include now, generateWorkflowExecutionID returns a legacy ID
// even though CRE is attached internally.
func TestGenerateWorkflowExecutionID_InactiveFlagUsesLegacyIDs(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)
	// Default FeatureMultiTrigger period is in 2100 → inactive with CRE present.

	reqID := "req-generate-legacy"
	refID := "trigger_0"
	wantLegacy, err := workflows.EncodeExecutionID(strings.TrimPrefix(testWorkflowID, "0x"), reqID) //nolint:staticcheck // SA1019
	require.NoError(t, err)
	wantLegacy = ensureHexPrefix(wantLegacy)

	// Intentionally pass t.Context() without WithCRE — the method under test must add it.
	got, isLegacy, err := handler.generateWorkflowExecutionID(
		t.Context(),
		testWorkflowID,
		testWorkflowOwner,
		"test-org",
		reqID,
		refID,
		lggr,
	)
	require.NoError(t, err)
	assert.True(t, isLegacy)
	assert.Equal(t, wantLegacy, got)
}
