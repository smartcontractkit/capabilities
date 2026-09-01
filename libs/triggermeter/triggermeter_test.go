package triggermeter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"
)

var testCfg = Config{
	Service:      "test-trigger",
	ResourcePool: "test_pool",
	ResourceType: "operations",
}

var testDep = resourcemanager.DeploymentIdentity{
	Product:         "cre-test",
	Tenant:          "mainline",
	NumericTenantID: "42",
	Environment:     "staging",
	Zone:            "wf-zone-a",
	NodeID:          "node-1",
}

// fakeEmitter captures MeterSnapshot emissions (this package's meters emit no
// records); a non-nil err simulates delivery failure.
type fakeEmitter struct {
	err       error
	emitCalls atomic.Int32
	snapshots []*meteringpb.MeterSnapshot
}

func (f *fakeEmitter) Emit(_ context.Context, body []byte, attrKVs ...any) error {
	f.emitCalls.Add(1)
	if f.err != nil {
		return f.err
	}
	for i := 0; i+1 < len(attrKVs); i += 2 {
		if attrKVs[i] == "beholder_entity" && attrKVs[i+1] == "metering.v1.MeterSnapshot" {
			var snapshot meteringpb.MeterSnapshot
			if err := proto.Unmarshal(body, &snapshot); err != nil {
				return err
			}
			f.snapshots = append(f.snapshots, &snapshot)
			return nil
		}
	}
	return nil
}

// fakeOrgResolver counts Get calls; configurable to error or panic.
type fakeOrgResolver struct {
	calls    atomic.Int32
	err      error
	doPanic  bool
	resolved string
}

func (f *fakeOrgResolver) Get(context.Context, string) (string, error) {
	f.calls.Add(1)
	if f.doPanic {
		panic("resolver exploded")
	}
	return f.resolved, f.err
}
func (f *fakeOrgResolver) Start(context.Context) error    { return nil }
func (f *fakeOrgResolver) Close() error                   { return nil }
func (f *fakeOrgResolver) Ready() error                   { return nil }
func (f *fakeOrgResolver) HealthReport() map[string]error { return nil }
func (f *fakeOrgResolver) Name() string                   { return "fakeOrgResolver" }

func newEnabledRM(t *testing.T, emitter resourcemanager.Emitter, clock clockwork.Clock) *resourcemanager.ResourceManager {
	t.Helper()
	return resourcemanager.NewResourceManager(logger.Test(t), resourcemanager.ResourceManagerConfig{
		MeterRecordsEnabled:   true,
		MeterSnapshotsEnabled: true,
		Emitter:               emitter,
		SnapshotInterval:      time.Minute,
		Clock:                 clock,
	})
}

func TestNew_IdentityStamping(t *testing.T) {
	t.Parallel()

	t.Run("full deployment + capability DON", func(t *testing.T) {
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, nil, nil)
		donID, err := tm.DonID()
		require.NoError(t, err)
		assert.Equal(t, "7", donID)
		assert.Equal(t, "cre-test", tm.base.Product)
		assert.Equal(t, "test-trigger", tm.base.Service)
		assert.Equal(t, "test_pool", tm.base.ResourcePool)
		assert.Equal(t, "node-1", tm.base.NodeID())
	})

	t.Run("empty product falls back to Config.Product", func(t *testing.T) {
		dep := testDep
		dep.Product = ""
		cfg := testCfg
		cfg.Product = "custom"
		tm := New(logger.Test(t), nil, dep, 0, cfg, nil, nil)
		assert.Equal(t, "custom", tm.base.Product)
	})

	t.Run("empty product and empty Config.Product falls back to DefaultProduct", func(t *testing.T) {
		dep := testDep
		dep.Product = ""
		tm := New(logger.Test(t), nil, dep, 0, testCfg, nil, nil)
		assert.Equal(t, DefaultProduct, tm.base.Product)
	})

	t.Run("zero capability DON leaves the DON dimension absent", func(t *testing.T) {
		tm := New(logger.Test(t), nil, testDep, 0, testCfg, nil, nil)
		_, err := tm.DonID()
		require.ErrorIs(t, err, ErrDonIDNotInitialised)
		assert.Equal(t, "node-1", tm.base.NodeID(), "the node dimension survives without a DON ID")
	})
}

// TestNilReceiverSafety asserts every method is a safe no-op on a nil meter:
// a nil *TriggerMeter is the metering-off posture for partially constructed
// components and tests.
func TestNilReceiverSafety(t *testing.T) {
	t.Parallel()
	var tm *TriggerMeter
	require.NoError(t, tm.Start(t.Context()))
	require.NoError(t, tm.Close())
	require.NoError(t, tm.Ready())
	assert.NotNil(t, tm.HealthReport())
	assert.Equal(t, "TriggerMeter", tm.Name())
	_, err := tm.DonID()
	require.ErrorIs(t, err, ErrDonIDNotInitialised)
	assert.Empty(t, tm.ResolveOrg(t.Context(), "owner"))
	assert.Nil(t, tm.GetUtilization(t.Context()))
}

// TestNilRMSafety asserts a meter constructed with a nil ResourceManager
// (metering off) no-ops on lifecycle but still serves identity (DonID feeds
// event labels regardless of metering state).
func TestNilRMSafety(t *testing.T) {
	t.Parallel()
	tm := New(logger.Test(t), nil, testDep, 7, testCfg, nil, nil)
	require.NoError(t, tm.Start(t.Context()))
	require.NoError(t, tm.Close())
	donID, err := tm.DonID()
	require.NoError(t, err)
	assert.Equal(t, "7", donID)
}

func TestStartClose_Lifecycle(t *testing.T) {
	t.Parallel()

	t.Run("start registers, snapshot tick polls, close stops", func(t *testing.T) {
		emitter := &fakeEmitter{}
		clock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
		rows := []SnapshotRow{{Value: 3, ResourceID: "res-1", OrgID: "org-1", DonID: ""}}
		tm := New(logger.Test(t), newEnabledRM(t, emitter, clock), testDep, 7, testCfg, nil,
			func(context.Context) []SnapshotRow { return rows })
		require.NoError(t, tm.Start(t.Context()))

		require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
		clock.Advance(time.Minute)
		require.Eventually(t, func() bool { return emitter.emitCalls.Load() >= 1 }, time.Second, time.Millisecond)

		require.NoError(t, tm.Close())
		require.Len(t, emitter.snapshots, 1)
		s := emitter.snapshots[0]
		assert.Equal(t, "res-1", s.GetUtilization()[0].GetResourceId())
		assert.Equal(t, "3", s.GetUtilization()[0].GetValue())
		assert.Equal(t, "org-1", s.GetUtilization()[0].GetOrgId())
		assert.Equal(t, "operations", s.GetUtilization()[0].GetResourceType())
		assert.Equal(t, "7", s.GetIdentity().GetDon().GetDonId())
	})

	t.Run("close without start is a no-op (never closes an unstarted RM)", func(t *testing.T) {
		tm := New(logger.Test(t), newEnabledRM(t, &fakeEmitter{}, nil), testDep, 7, testCfg, nil, nil)
		require.NoError(t, tm.Close())
	})

	t.Run("double start and double close are idempotent", func(t *testing.T) {
		tm := New(logger.Test(t), newEnabledRM(t, &fakeEmitter{}, nil), testDep, 7, testCfg, nil, nil)
		require.NoError(t, tm.Start(t.Context()))
		require.NoError(t, tm.Start(t.Context()))
		require.NoError(t, tm.Close())
		require.NoError(t, tm.Close())
	})
}

func TestResolveOrg(t *testing.T) {
	t.Parallel()

	t.Run("context CRE org wins without a resolver call", func(t *testing.T) {
		resolver := &fakeOrgResolver{resolved: "org-from-resolver"}
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, resolver, nil)
		ctx := contexts.WithCRE(t.Context(), contexts.CRE{Org: "org-from-ctx"})
		assert.Equal(t, "org-from-ctx", tm.ResolveOrg(ctx, "owner"))
		assert.Zero(t, resolver.calls.Load())
	})

	t.Run("resolver fallback", func(t *testing.T) {
		resolver := &fakeOrgResolver{resolved: "org-42"}
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, resolver, nil)
		assert.Equal(t, "org-42", tm.ResolveOrg(t.Context(), "owner"))
		assert.Equal(t, int32(1), resolver.calls.Load())
	})

	t.Run("resolver error fails open to empty", func(t *testing.T) {
		resolver := &fakeOrgResolver{err: errors.New("boom")}
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, resolver, nil)
		assert.Empty(t, tm.ResolveOrg(t.Context(), "owner"))
	})

	t.Run("resolver panic fails open to empty", func(t *testing.T) {
		resolver := &fakeOrgResolver{doPanic: true}
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, resolver, nil)
		assert.Empty(t, tm.ResolveOrg(t.Context(), "owner"))
	})

	t.Run("nil resolver or empty owner resolves to empty", func(t *testing.T) {
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, nil, nil)
		assert.Empty(t, tm.ResolveOrg(t.Context(), "owner"))
		resolver := &fakeOrgResolver{resolved: "org"}
		tm = New(logger.Test(t), nil, testDep, 7, testCfg, resolver, nil)
		assert.Empty(t, tm.ResolveOrg(t.Context(), ""))
		assert.Zero(t, resolver.calls.Load())
	})
}

func TestGetUtilization(t *testing.T) {
	t.Parallel()

	t.Run("maps rows with per-row DON re-stamp", func(t *testing.T) {
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, nil, func(context.Context) []SnapshotRow {
			return []SnapshotRow{
				{Value: 1, ResourceID: "a", OrgID: "org-a"},
				{Value: 5, ResourceID: "b", OrgID: "org-b", DonID: "99"},
			}
		})
		entries := tm.GetUtilization(t.Context())
		require.Len(t, entries, 2)
		assert.Equal(t, "7", entries[0].Identity.DonID(), "no per-row DON: base identity used")
		assert.Equal(t, "99", entries[1].Identity.DonID(), "per-row DON re-stamps the identity")
		assert.Equal(t, "node-1", entries[1].Identity.NodeID(), "the node dimension survives the re-stamp")
		assert.Equal(t, "b", entries[1].Utilizations[0].GetResourceId())
		assert.Equal(t, "5", entries[1].Utilizations[0].GetValue())
		assert.Equal(t, "org-b", entries[1].Utilizations[0].GetOrgId())
		assert.Equal(t, "operations", entries[1].Utilizations[0].GetResourceType())
	})

	t.Run("cancelled context short-circuits", func(t *testing.T) {
		called := false
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, nil, func(context.Context) []SnapshotRow {
			called = true
			return nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		assert.Nil(t, tm.GetUtilization(ctx))
		assert.False(t, called)
	})

	t.Run("nil snapshot source yields nil", func(t *testing.T) {
		tm := New(logger.Test(t), nil, testDep, 7, testCfg, nil, nil)
		assert.Nil(t, tm.GetUtilization(t.Context()))
	})
}

// TestFailOpen_ErroringEmitter asserts an emitter that fails on every call
// never disturbs the meter's lifecycle.
func TestFailOpen_ErroringEmitter(t *testing.T) {
	t.Parallel()
	emitter := &fakeEmitter{err: errors.New("collector down")}
	clock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	tm := New(logger.Test(t), newEnabledRM(t, emitter, clock), testDep, 7, testCfg, nil,
		func(context.Context) []SnapshotRow { return []SnapshotRow{{Value: 1, ResourceID: "r"}} })
	require.NoError(t, tm.Start(t.Context()))
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)
	require.Eventually(t, func() bool { return emitter.emitCalls.Load() >= 1 }, time.Second, time.Millisecond)
	require.NoError(t, tm.Close())
	assert.Empty(t, emitter.snapshots)
}

func TestHealthReportAndName(t *testing.T) {
	t.Parallel()
	tm := New(logger.Test(t), newEnabledRM(t, &fakeEmitter{}, nil), testDep, 7, testCfg, nil, nil)
	assert.Equal(t, "TriggerMeter.test-trigger", tm.Name())
	report := tm.HealthReport()
	require.Contains(t, report, "TriggerMeter.test-trigger")
	require.NoError(t, tm.Start(t.Context()))
	assert.Greater(t, len(tm.HealthReport()), 1, "a started meter folds in the ResourceManager's report")
	require.NoError(t, tm.Close())
}
