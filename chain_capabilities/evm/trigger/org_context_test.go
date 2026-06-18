package trigger

import (
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
)

func creSettingsJSON(t *testing.T, json string) settings.Getter {
	t.Helper()
	g, err := settings.NewJSONGetter([]byte(json))
	require.NoError(t, err)
	return g
}

func TestDeliverLogReliably_ResolvesOrgForRetransmit(t *testing.T) {
	store := capabilities.NewMemEventStore()
	lggr := logger.Test(t)
	settingsJSON := `{
		"global": {
			"PerOrg": {"BaseTriggerRetransmitEnabled": "false"}
		},
		"org": {
			"staging-org": {
				"PerOrg": {"BaseTriggerRetransmitEnabled": "true"}
			}
		}
	}`
	g := creSettingsJSON(t, settingsJSON)

	resolver := &mockOrgResolver{}
	resolver.On("Get", mock.Anything, "0xOwner").Return("staging-org", nil)

	baseTrigger, err := capabilities.NewBaseTriggerCapabilityWithCRESettings(
		t.Context(), store, func() *evmcappb.Log { return &evmcappb.Log{} }, lggr, "cap", g)
	require.NoError(t, err)
	require.NoError(t, baseTrigger.Start(t.Context()))
	t.Cleanup(baseTrigger.Stop)

	inbox := make(chan capabilities.TriggerAndId[*evmcappb.Log], 1)
	baseTrigger.RegisterTrigger("trig-1", inbox)

	lts := &LogTriggerService{
		lggr:        lggr,
		baseTrigger: baseTrigger,
		orgResolver: resolver,
	}

	protoLog := &evmcappb.Log{}
	log := &evmtypes.Log{BlockNumber: big.NewInt(1)}
	telemetryContext := monitoring.TelemetryContext{
		RequestMetadata: capabilities.RequestMetadata{
			WorkflowOwner: "0xOwner",
			WorkflowID:    "wf-1",
		},
	}

	// Registration ctx without org (PropagateOrgID was off at register time).
	regMeta := capabilities.RequestMetadata{WorkflowOwner: "0xOwner", WorkflowID: "wf-1"}
	regCtx := regMeta.ContextWithCRE(t.Context())
	require.Empty(t, contexts.CREValue(regCtx).Org)

	var sentCount int
	var needsUpdate bool
	triggerState := logTriggerState{unfinalizedSentEventIDs: make(map[string]*big.Int)}
	lts.deliverLogReliably(regCtx, telemetryContext, "trig-1", protoLog, "event-1",
		big.NewInt(0), log, &triggerState, &sentCount, &needsUpdate)

	require.Equal(t, 1, sentCount)
	recs, err := store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, recs, 1)
	require.Equal(t, "staging-org", recs[0].OrgID)
	resolver.AssertExpectations(t)
}

// mockOrgResolver uses testify mock for expectation assertions in integration tests.
type mockOrgResolver struct {
	mock.Mock
}

func (m *mockOrgResolver) Get(ctx context.Context, owner string) (string, error) {
	args := m.Called(ctx, owner)
	return args.String(0), args.Error(1)
}
func (m *mockOrgResolver) Start(context.Context) error { return nil }
func (m *mockOrgResolver) Close() error                { return nil }
func (m *mockOrgResolver) HealthReport() map[string]error {
	return map[string]error{}
}
func (m *mockOrgResolver) Ready() error { return nil }
func (m *mockOrgResolver) Name() string { return "mockOrgResolver" }

var _ orgresolver.OrgResolver = (*mockOrgResolver)(nil)
