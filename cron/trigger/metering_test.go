package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	crontypedapi "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"
)

// fakeMeterEmitter captures metering records delivered through a real
// ResourceManager, so tests assert on exactly the bytes production would emit.
// It demultiplexes MeterRecord and MeterSnapshot messages by their beholder
// entity attribute. A non-nil err simulates delivery failure: nothing is
// recorded.
type fakeMeterEmitter struct {
	mu        sync.Mutex
	err       error
	records   []*meteringpb.MeterRecord
	snapshots []*meteringpb.MeterSnapshot
}

func (f *fakeMeterEmitter) Emit(_ context.Context, body []byte, attrKVs ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if attrEntity(attrKVs) == "metering.v1.MeterSnapshot" {
		snapshot := &meteringpb.MeterSnapshot{}
		if err := proto.Unmarshal(body, snapshot); err != nil {
			return err
		}
		f.snapshots = append(f.snapshots, snapshot)
		return nil
	}
	record := &meteringpb.MeterRecord{}
	if err := proto.Unmarshal(body, record); err != nil {
		return err
	}
	f.records = append(f.records, record)
	return nil
}

// attrEntity extracts the beholder entity attribute value from the variadic
// key/value attrs the ResourceManager passes to Emit.
func attrEntity(attrKVs []any) string {
	for i := 0; i+1 < len(attrKVs); i += 2 {
		if k, ok := attrKVs[i].(string); ok && k == "beholder_entity" {
			if v, ok := attrKVs[i+1].(string); ok {
				return v
			}
		}
	}
	return ""
}

func (f *fakeMeterEmitter) Records() []*meteringpb.MeterRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*meteringpb.MeterRecord(nil), f.records...)
}

func (f *fakeMeterEmitter) Snapshots() []*meteringpb.MeterSnapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*meteringpb.MeterSnapshot(nil), f.snapshots...)
}

// meteredTestDeps are the host-injected dependencies used by metering tests.
// The DON dimension still arrives via the Initialise channel; the
// deployment/node dimensions now arrive via loop.EnvConfig (meteredTestDeployment).
var meteredTestDeps = core.StandardCapabilitiesDependencies{
	CapabilityDonID: 7,
}

// meteredTestDeployment is the deployment/node identity that main would source
// from loop.EnvConfig and set on the service before Initialise.
var meteredTestDeployment = resourcemanager.DeploymentIdentity{
	Product:         "cre-mainline",
	Tenant:          "mainline",
	NumericTenantID: "42",
	Environment:     "staging",
	Zone:            "wf-zone-a",
	NodeID:          "csa-pubkey-1",
}

// expectedBaseIdentity is the base identity the Service builds from
// meteredTestDeps (resource_id left empty; set per trigger).
var expectedBaseIdentity = resourcemanager.ResourceIdentity{
	Product:         "cre-mainline",
	Tenant:          "mainline",
	NumericTenantID: "42",
	Environment:     "staging",
	Zone:            "wf-zone-a",
	Don:             &resourcemanager.DonIdentity{DonID: "7", NodeID: "csa-pubkey-1"},
	Service:         "cron-trigger",
	ResourcePool:    "trigger_registrations",
}

// newMeteredTriggerService builds an initialised trigger service whose
// ResourceManager is enabled and wired to emitter, with identity sourced from
// meteredTestDeps. Snapshots use a fake clock so tests advance the tick
// deterministically.
func newMeteredTriggerService(t *testing.T, clock clockwork.Clock, emitter resourcemanager.Emitter) (*Service, *resourcemanager.ResourceManager, *clockwork.FakeClock) {
	t.Helper()

	fakeClock, ok := clock.(*clockwork.FakeClock)
	if !ok {
		fakeClock = clockwork.NewFakeClockAt(clock.Now())
		clock = fakeClock
	}

	meters := resourcemanager.NewResourceManager(logger.Nop(), resourcemanager.ResourceManagerConfig{
		MeterRecordsEnabled:   true,
		MeterSnapshotsEnabled: true,
		Emitter:               emitter,
		SnapshotInterval:      time.Minute,
		Clock:                 clock,
	})
	ts, err := NewTriggerService(logger.Nop(), clock, limits.Factory{}, meters)
	require.NoError(t, err)
	ts.Deployment = meteredTestDeployment

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)

	deps := meteredTestDeps
	deps.Config = string(config)
	require.NoError(t, ts.Initialise(t.Context(), deps))

	return ts, meters, fakeClock
}

func TestCronTrigger_Metering_ReserveAndRelease(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	emitter := &fakeMeterEmitter{}
	ts, _, _ := newMeteredTriggerService(t, fakeClock, emitter)

	metadata := capabilities.RequestMetadata{
		WorkflowID:    workflowID1,
		WorkflowOwner: "0xOwner-1",
	}
	ch, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	records := emitter.Records()
	require.Len(t, records, 1, "expected exactly one RESERVE on successful registration")
	reserve := records[0]
	assert.Equal(t, meteringpb.MeterAction_METER_ACTION_RESERVE, reserve.GetAction())

	// Identity is populated from host-injected deps and points at the cron
	// resource pool. Per-trigger fields are carried on utilization.
	id := reserve.GetIdentity()
	require.NotNil(t, id)
	assert.Equal(t, "cre-mainline", id.GetProduct())
	assert.Equal(t, "mainline", id.GetTenant())
	assert.Equal(t, "42", id.GetNumericTenantId())
	assert.Equal(t, "staging", id.GetEnvironment())
	assert.Equal(t, "wf-zone-a", id.GetZone())
	assert.Equal(t, "7", id.GetDon().GetDonId())
	assert.Equal(t, "csa-pubkey-1", id.GetDon().GetNodeId())
	assert.Equal(t, "cron-trigger", id.GetService())
	assert.Equal(t, "trigger_registrations", id.GetResourcePool())

	require.Len(t, reserve.GetUtilizations(), 1)
	assert.Equal(t, "1", reserve.GetUtilizations()[0].GetValue())
	assert.Equal(t, "operations", reserve.GetUtilizations()[0].GetResourceType())
	assert.Equal(t, triggerID1, reserve.GetUtilizations()[0].GetResourceId())

	// Each cron tick re-Writes the trigger to reschedule it; the Write
	// happens before the channel send, so after receiving the event the
	// callback path has fully run. It must not emit.
	for range 3 {
		fakeClock.Advance(time.Second)
		<-ch
	}
	require.Len(t, emitter.Records(), 1, "cron tick callbacks must not emit meter records")

	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond}))
	records = emitter.Records()
	require.Len(t, records, 2, "expected exactly one RELEASE on unregistration")
	release := records[1]
	assert.Equal(t, meteringpb.MeterAction_METER_ACTION_RELEASE, release.GetAction())
	assert.Equal(t, reserve.GetUtilizations()[0].GetResourceId(), release.GetUtilizations()[0].GetResourceId())
	require.Len(t, release.GetUtilizations(), 1)
	assert.Equal(t, "1", release.GetUtilizations()[0].GetValue())

	require.NoError(t, ts.Close())
}

func TestCronTrigger_Metering_NoEmitOnFailedPaths(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClock()
	emitter := &fakeMeterEmitter{}
	ts, _, _ := newMeteredTriggerService(t, fakeClock, emitter)

	metadata := capabilities.RequestMetadata{WorkflowID: workflowID1, WorkflowOwner: "owner-1"}

	// Invalid schedule: registration fails before allocation, nothing emitted.
	_, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: "not-a-schedule"})
	require.NotNil(t, capErr)
	require.Empty(t, emitter.Records())

	// Unregistering a trigger that was never registered releases nothing.
	require.Nil(t, ts.UnregisterTrigger(t.Context(), "missing", metadata, &crontypedapi.Config{Schedule: everySecond}))
	require.Empty(t, emitter.Records())

	// Duplicate registration fails and must not double-RESERVE.
	_, capErr = ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)
	_, capErr = ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.NotNil(t, capErr)
	require.Len(t, emitter.Records(), 1)

	// Unregister to avoid a graceful-close RELEASE from interfering with the
	// single-RESERVE assertion intent.
	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond}))
	require.NoError(t, ts.Close())
}

func TestCronTrigger_Metering_FailOpen(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClock()
	emitter := &fakeMeterEmitter{err: errors.New("collector unavailable")}
	ts, _, _ := newMeteredTriggerService(t, fakeClock, emitter)

	metadata := capabilities.RequestMetadata{WorkflowID: workflowID1, WorkflowOwner: "owner-1"}

	// Registration and unregistration succeed even though every emission fails.
	ch, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	fakeClock.Advance(time.Second)
	<-ch // trigger still fires

	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond}))
	require.Empty(t, emitter.Records())

	require.NoError(t, ts.Close())
}

// TestCronTrigger_Metering_Snapshot asserts the Service implements Meterable
// such that a forced snapshot emits one MeterSnapshot per active trigger, each
// carrying the full base identity and per-trigger utilization.
func TestCronTrigger_Metering_Snapshot(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	emitter := &fakeMeterEmitter{}
	ts, rm, clock := newMeteredTriggerService(t, fakeClock, emitter)
	_ = rm

	metadata1 := capabilities.RequestMetadata{WorkflowID: workflowID1, WorkflowOwner: "0xOwner-1"}
	_, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata1, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	metadata2 := capabilities.RequestMetadata{WorkflowID: "workflow-id-2", WorkflowOwner: "owner-2"}
	const triggerID2 = "test-id-2"
	_, capErr = ts.RegisterTrigger(t.Context(), triggerID2, metadata2, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)

	// One MeterSnapshot per active trigger, value 1, full per-resource identity.
	require.Eventually(t, func() bool {
		return len(emitter.Snapshots()) == 2
	}, time.Second, time.Millisecond)
	snapshots := emitter.Snapshots()
	require.Len(t, snapshots, 2, "one MeterSnapshot per active trigger per tick")

	byTrigger := map[string]*meteringpb.MeterSnapshot{}
	for _, s := range snapshots {
		byTrigger[s.GetUtilization()[0].GetResourceId()] = s
	}

	s1 := byTrigger[triggerID1]
	require.NotNil(t, s1)
	assert.Equal(t, "1", s1.GetUtilization()[0].GetValue())
	assert.Equal(t, "cron-trigger", s1.GetIdentity().GetService())
	assert.Equal(t, "trigger_registrations", s1.GetIdentity().GetResourcePool())
	assert.Equal(t, "operations", s1.GetUtilization()[0].GetResourceType())

	s2 := byTrigger[triggerID2]
	require.NotNil(t, s2)
	assert.Equal(t, "1", s2.GetUtilization()[0].GetValue())
	assert.Equal(t, triggerID2, s2.GetUtilization()[0].GetResourceId())

	require.NoError(t, ts.Close())
}

// TestCronTrigger_Metering_GracefulCloseReleases asserts that Close drains a
// RELEASE for every still-active registration, so a graceful shutdown does not
// leak reservations in billing.
func TestCronTrigger_Metering_GracefulCloseReleases(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	emitter := &fakeMeterEmitter{}
	ts, _, _ := newMeteredTriggerService(t, fakeClock, emitter)

	metadata1 := capabilities.RequestMetadata{WorkflowID: workflowID1, WorkflowOwner: "0xOwner-1"}
	_, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata1, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	metadata2 := capabilities.RequestMetadata{WorkflowID: "workflow-id-2", WorkflowOwner: "owner-2"}
	const triggerID2 = "test-id-2"
	_, capErr = ts.RegisterTrigger(t.Context(), triggerID2, metadata2, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	// Two RESERVEs so far.
	require.Len(t, emitter.Records(), 2)

	require.NoError(t, ts.Close())

	// Close drained a RELEASE for each active trigger.
	records := emitter.Records()
	require.Len(t, records, 4, "two RESERVEs + one RELEASE per active trigger on graceful close")

	releases := map[string]*meteringpb.MeterRecord{}
	for _, r := range records[2:] {
		require.Equal(t, meteringpb.MeterAction_METER_ACTION_RELEASE, r.GetAction())
		releases[r.GetUtilizations()[0].GetResourceId()] = r
	}
	require.Contains(t, releases, triggerID1)
	require.Contains(t, releases, triggerID2)
	assert.Equal(t, "1", releases[triggerID1].GetUtilizations()[0].GetValue())
}

// TestCronTrigger_Metering_DonIDFallback asserts the DON ID falls back to the
// consumer workflow's DON when the host has not injected a capability DON ID.
func TestCronTrigger_Metering_DonIDFallback(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	emitter := &fakeMeterEmitter{}

	meters := resourcemanager.NewResourceManager(logger.Nop(), resourcemanager.ResourceManagerConfig{
		MeterRecordsEnabled: true,
		Emitter:             emitter,
	})
	ts, err := NewTriggerService(logger.Nop(), fakeClock, limits.Factory{}, meters)
	require.NoError(t, err)

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)

	// No CapabilityDonID injected (zero) → fall back to WorkflowDonID at emit.
	require.NoError(t, ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: string(config)}))

	metadata := capabilities.RequestMetadata{WorkflowID: workflowID1, WorkflowOwner: "owner-1", WorkflowDonID: 42}
	_, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	records := emitter.Records()
	require.Len(t, records, 1)
	assert.Equal(t, "42", records[0].GetIdentity().GetDon().GetDonId(), "DON ID falls back to WorkflowDonID")
	// Product falls back to the cron constant when the host injects none.
	assert.Equal(t, "cre", records[0].GetIdentity().GetProduct())

	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond}))
	require.NoError(t, ts.Close())
}
