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

	"github.com/smartcontractkit/capabilities/libs/triggermeter"
)

// fakeMeterEmitter captures metering emissions delivered through a real
// ResourceManager, so tests assert on exactly the bytes production would emit.
// It demultiplexes MeterRecord and MeterSnapshot messages by their beholder
// entity attribute. A non-nil err simulates delivery failure: nothing is
// recorded.
type fakeMeterEmitter struct {
	mu            sync.Mutex
	err           error
	records       []*meteringpb.MeterRecord
	recordDomains []string
	snapshots     []*meteringpb.MeterSnapshot
}

func (f *fakeMeterEmitter) Emit(_ context.Context, body []byte, attrKVs ...any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	if attrValue(attrKVs, "beholder_entity") == "metering.v1.MeterSnapshot" {
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
	f.recordDomains = append(f.recordDomains, attrValue(attrKVs, "beholder_domain"))
	return nil
}

// attrValue extracts a beholder attribute value by key from the variadic
// key/value attrs the ResourceManager passes to Emit.
func attrValue(attrKVs []any, key string) string {
	for i := 0; i+1 < len(attrKVs); i += 2 {
		if k, ok := attrKVs[i].(string); ok && k == key {
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
	NodeID:          "clp-cre-wf-zone-a-1",
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

// TestCronTrigger_Metering_NoRecords is the core snapshot-only invariant: a
// full registration lifecycle — register, tick callbacks, failed paths,
// unregister, re-register (the restart shape) — emits ZERO MeterRecords.
// Trigger capabilities bill exclusively through snapshots: the level rises
// when a registration appears in the next snapshot and is released by its
// absence, so there are no deltas to re-emit on restart and nothing for the
// billing consumer to dedup.
func TestCronTrigger_Metering_NoRecords(t *testing.T) {
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
	require.Empty(t, emitter.Records(), "registration must not emit meter records")

	// Failed paths emit nothing either.
	_, capErr = ts.RegisterTrigger(t.Context(), "bad-schedule", metadata, &crontypedapi.Config{Schedule: "not-a-schedule"})
	require.NotNil(t, capErr)
	_, capErr = ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.NotNil(t, capErr, "duplicate registration fails")
	require.Nil(t, ts.UnregisterTrigger(t.Context(), "missing", metadata, &crontypedapi.Config{Schedule: everySecond}))
	require.Empty(t, emitter.Records())

	// Each cron tick re-Writes the trigger to reschedule it; the Write happens
	// before the channel send, so after receiving the event the callback path
	// has fully run. It must not emit.
	for range 3 {
		fakeClock.Advance(time.Second)
		<-ch
	}
	require.Empty(t, emitter.Records(), "cron tick callbacks must not emit meter records")

	// Unregister then re-register the same trigger — the shape of an engine
	// restart re-registering its triggers. Still nothing on the record stream.
	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond}))
	_, capErr = ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)
	require.Empty(t, emitter.Records(), "restart-shaped re-registration must not emit meter records")

	require.NoError(t, ts.Close())
	require.Empty(t, emitter.Records())
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

// TestCronTrigger_Metering_NilMeterEquivalence asserts the fail-open
// equivalence contract: with metering off entirely (nil ResourceManager), the
// register/fire/unregister lifecycle behaves identically to the metered path.
func TestCronTrigger_Metering_NilMeterEquivalence(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClock()
	ts, err := NewTriggerService(logger.Nop(), fakeClock, limits.Factory{}, nil)
	require.NoError(t, err)

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)
	require.NoError(t, ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: string(config)}))

	metadata := capabilities.RequestMetadata{WorkflowID: workflowID1, WorkflowOwner: "owner-1"}
	ch, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	fakeClock.Advance(time.Second)
	<-ch

	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond}))
	require.NoError(t, ts.Close())
}

// TestCronTrigger_Metering_Snapshot asserts the snapshot surface end to end: a
// tick emits one MeterSnapshot per active trigger carrying the full identity
// and per-trigger utilization, and an unregistered trigger is released by its
// absence from the following tick.
func TestCronTrigger_Metering_Snapshot(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	emitter := &fakeMeterEmitter{}
	ts, _, clock := newMeteredTriggerService(t, fakeClock, emitter)

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
	assert.Equal(t, "operations", s1.GetUtilization()[0].GetResourceType())

	// The snapshot identity carries the deployment dimensions from
	// loop.EnvConfig and the host-injected capability DON.
	id := s1.GetIdentity()
	require.NotNil(t, id)
	assert.Equal(t, "cre-mainline", id.GetProduct())
	assert.Equal(t, "mainline", id.GetTenant())
	assert.Equal(t, "42", id.GetNumericTenantId())
	assert.Equal(t, "staging", id.GetEnvironment())
	assert.Equal(t, "wf-zone-a", id.GetZone())
	assert.Equal(t, "7", id.GetDon().GetDonId())
	assert.Equal(t, "clp-cre-wf-zone-a-1", id.GetDon().GetNodeId())
	assert.Equal(t, "cron-trigger", id.GetService())
	assert.Equal(t, "trigger_registrations", id.GetResourcePool())
	capDonID, donErr := ts.meter.DonID()
	require.NoError(t, donErr)
	assert.Equal(t, capDonID, id.GetDon().GetDonId())

	s2 := byTrigger[triggerID2]
	require.NotNil(t, s2)
	assert.Equal(t, "1", s2.GetUtilization()[0].GetValue())
	assert.Equal(t, triggerID2, s2.GetUtilization()[0].GetResourceId())

	// Release-by-absence: after unregistering trigger 2, the next tick
	// snapshots only trigger 1.
	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID2, metadata2, &crontypedapi.Config{Schedule: everySecond}))
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)
	require.Eventually(t, func() bool {
		return len(emitter.Snapshots()) == 3
	}, time.Second, time.Millisecond)
	last := emitter.Snapshots()[2]
	assert.Equal(t, triggerID1, last.GetUtilization()[0].GetResourceId(),
		"an unregistered trigger is released by its absence from the next snapshot")

	// The snapshot stream is the only metering surface: no records, ever.
	require.Empty(t, emitter.Records())

	require.NoError(t, ts.Close())
}

// TestCronTrigger_Metering_NoShutdownEmissions asserts that a graceful Close
// emits NO metering at all. Process-lifecycle emissions are deleted by design:
// billing releases each still-active registration by its absence from the next
// snapshot, not by a shutdown drain.
func TestCronTrigger_Metering_NoShutdownEmissions(t *testing.T) {
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

	require.NoError(t, ts.Close())

	require.Empty(t, emitter.Records(), "graceful close must emit no meter records")
	require.Empty(t, emitter.Snapshots(), "no snapshot tick ran; close must not force one")
}

// TestCronTrigger_Metering_DonIDNotInitialised asserts that when the host has
// not injected a capability DON ID, snapshots are still emitted but with the
// DON dimension absent — the consumer workflow's DON ID is never substituted —
// and the meter's DonID returns ErrDonIDNotInitialised so callers that want a
// best-effort value (event labels, CRE-4409) degrade explicitly themselves.
func TestCronTrigger_Metering_DonIDNotInitialised(t *testing.T) {
	t.Parallel()

	fakeClock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	emitter := &fakeMeterEmitter{}

	meters := resourcemanager.NewResourceManager(logger.Nop(), resourcemanager.ResourceManagerConfig{
		MeterRecordsEnabled:   true,
		MeterSnapshotsEnabled: true,
		Emitter:               emitter,
		SnapshotInterval:      time.Minute,
		Clock:                 fakeClock,
	})
	ts, err := NewTriggerService(logger.Nop(), fakeClock, limits.Factory{}, meters)
	require.NoError(t, err)

	config, err := json.Marshal(Config{FastestScheduleIntervalSeconds: 1})
	require.NoError(t, err)

	// No CapabilityDonID injected (zero) → the DON dimension stays absent.
	require.NoError(t, ts.Initialise(t.Context(), core.StandardCapabilitiesDependencies{Config: string(config)}))

	_, donErr := ts.meter.DonID()
	require.ErrorIs(t, donErr, triggermeter.ErrDonIDNotInitialised)

	metadata := capabilities.RequestMetadata{WorkflowID: workflowID1, WorkflowOwner: "owner-1", WorkflowDonID: 42}
	_, capErr := ts.RegisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond})
	require.Nil(t, capErr)

	require.NoError(t, fakeClock.BlockUntilContext(t.Context(), 1))
	fakeClock.Advance(time.Minute)
	require.Eventually(t, func() bool {
		return len(emitter.Snapshots()) == 1
	}, time.Second, time.Millisecond)

	snapshot := emitter.Snapshots()[0]
	assert.Empty(t, snapshot.GetIdentity().GetDon().GetDonId(),
		"the consumer workflow's DON ID must never be substituted for the capability DON")
	// Product falls back to the shared trigger default when the host injects none.
	assert.Equal(t, "cre", snapshot.GetIdentity().GetProduct())

	require.Nil(t, ts.UnregisterTrigger(t.Context(), triggerID1, metadata, &crontypedapi.Config{Schedule: everySecond}))
	require.NoError(t, ts.Close())
}
