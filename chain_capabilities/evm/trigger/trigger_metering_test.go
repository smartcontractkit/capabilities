package trigger

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"

	"github.com/smartcontractkit/capabilities/libs/triggermeter"
)

const testChainSelector = "5009297550715157269"

// testDeployment is the deployment/node identity the metering tests build
// their meter with (production sources it from loop.EnvConfig). It carries
// every coarse dimension so the tests can assert each one is populated on the
// emitted snapshots.
var testDeployment = resourcemanager.DeploymentIdentity{
	Product:         "cre",
	Tenant:          "mainline",
	NumericTenantID: "42",
	Environment:     "staging",
	Zone:            "wf-zone-a",
	NodeID:          "csa-pubkey-hex",
}

// fakeMeterEmitter captures MeterRecords and MeterSnapshots emitted through the
// ResourceManager. The two message types are distinguished by the entity
// attribute the emitter is called with. The manager emits one MeterSnapshot
// per active resource, so snapshots accumulates one message per resource.
type fakeMeterEmitter struct {
	err           error
	emitCalls     int
	records       []*meteringpb.MeterRecord
	recordDomains []string
	snapshots     []*meteringpb.MeterSnapshot
}

func (f *fakeMeterEmitter) Emit(_ context.Context, body []byte, attrKVs ...any) error {
	f.emitCalls++
	if f.err != nil {
		return f.err
	}
	if isSnapshotEmit(attrKVs) {
		var snapshot meteringpb.MeterSnapshot
		if err := proto.Unmarshal(body, &snapshot); err != nil {
			return err
		}
		f.snapshots = append(f.snapshots, &snapshot)
		return nil
	}
	var record meteringpb.MeterRecord
	if err := proto.Unmarshal(body, &record); err != nil {
		return err
	}
	f.records = append(f.records, &record)
	f.recordDomains = append(f.recordDomains, attrString(attrKVs, beholder.AttrKeyDomain))
	return nil
}

// attrString returns the string value for key in the alternating key/value
// attrs the ResourceManager passes to Emit, or "" if absent.
func attrString(attrKVs []any, key string) string {
	for i := 0; i+1 < len(attrKVs); i += 2 {
		if attrKVs[i] == key {
			if v, ok := attrKVs[i+1].(string); ok {
				return v
			}
		}
	}
	return ""
}

// isSnapshotEmit reports whether the emitter attributes name the MeterSnapshot
// entity, so the fake can demux the two message types off the same Emit method.
// The key is beholder.AttrKeyEntity ("beholder_entity") and the value is the
// snapshot entity constant the ResourceManager emits with.
func isSnapshotEmit(attrKVs []any) bool {
	for i := 0; i+1 < len(attrKVs); i += 2 {
		if attrKVs[i] == beholder.AttrKeyEntity && attrKVs[i+1] == "metering.v1.MeterSnapshot" {
			return true
		}
	}
	return false
}

// newMeteredTriggerObject builds a LogTriggerService whose meter wraps an
// enabled ResourceManager wired to a fake emitter. The poll interval is
// stretched so the polling goroutine stays quiet; metering happens on the
// snapshot tick only (the EVM trigger emits no MeterRecord deltas).
func newMeteredTriggerObject(t *testing.T, mockEVM *evmmock.EVMService, store LogTriggerStore) (*LogTriggerService, *fakeMeterEmitter, *clockwork.FakeClock) {
	t.Helper()
	lts := createTriggerObject(t, mockEVM, store)
	lts.logTriggerPollInterval = time.Hour
	emitter := &fakeMeterEmitter{}
	clock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	rm := resourcemanager.NewResourceManager(logger.Test(t),
		resourcemanager.ResourceManagerConfig{
			MeterRecordsEnabled:   true,
			MeterSnapshotsEnabled: true,
			Emitter:               emitter,
			SnapshotInterval:      time.Minute,
			Clock:                 clock,
		})
	lts.meter = triggermeter.New(logger.Test(t), rm, testDeployment, 42, meteringConfig, nil, lts.snapshotRows)
	lts.chainSelector = testChainSelector
	return lts, emitter, clock
}

// startMeter starts the meter (RM + snapshot registration) and tears it down
// on test cleanup, for tests that drive snapshot ticks directly without the
// full service lifecycle.
func startMeter(t *testing.T, lts *LogTriggerService) {
	t.Helper()
	require.NoError(t, lts.meter.Start(t.Context()))
	t.Cleanup(func() { require.NoError(t, lts.meter.Close()) })
}

// meteringTestInput is a registration request with two filter addresses, so
// tests can tell an address count apart from a hardcoded 1.
func meteringTestInput() *evmcappb.FilterLogTriggerRequest {
	return &evmcappb.FilterLogTriggerRequest{
		Addresses: [][]byte{expectedAddress, bytes.Repeat([]byte{0x42}, evmtypes.AddressLength)},
		Topics:    topicsWithEventSig0,
	}
}

// assertBaseIdentity checks the six coarse dimensions + service/resource_pool
// on the emitted snapshot identity, proving the host-injected identity is
// carried.
func assertBaseIdentity(t *testing.T, id *meteringpb.ResourceIdentity) {
	t.Helper()
	require.NotNil(t, id)
	require.Equal(t, "cre", id.GetProduct())
	require.Equal(t, "mainline", id.GetTenant())
	require.Equal(t, "42", id.GetNumericTenantId())
	require.Equal(t, "staging", id.GetEnvironment())
	require.Equal(t, "wf-zone-a", id.GetZone())
	require.Equal(t, "42", id.GetDon().GetDonId())
	require.Equal(t, "csa-pubkey-hex", id.GetDon().GetNodeId())
	require.Equal(t, meteringConfig.Service, id.GetService())
	require.Equal(t, meteringConfig.ResourcePool, id.GetResourcePool())
}

// expectedPhysicalFilterID recomputes the physical filter id for the metering
// test input via the production helper, so the tests assert against the real
// canonicalization rather than a frozen literal.
func expectedPhysicalFilterID(t *testing.T, input *evmcappb.FilterLogTriggerRequest) string {
	t.Helper()
	svc := &LogTriggerService{}
	eventSigs, t2, t3, t4 := svc.getTopics(input)
	addrs, err := evmservice.ConvertAddressesFromProto(input.GetAddresses())
	require.NoError(t, err)
	sigs, err := evmservice.ConvertHashesFromProto(eventSigs)
	require.NoError(t, err)
	h2, err := evmservice.ConvertHashesFromProto(t2)
	require.NoError(t, err)
	h3, err := evmservice.ConvertHashesFromProto(t3)
	require.NoError(t, err)
	h4, err := evmservice.ConvertHashesFromProto(t4)
	require.NoError(t, err)
	return physicalFilterID(testChainSelector, addrs, sigs, h2, h3, h4)
}

// TestLogTriggerMetering_NoRecords is the core snapshot-only invariant: the
// full registration lifecycle — register, shared-filter register, unregister,
// re-register (the restart shape), failed paths — emits ZERO MeterRecords.
// EVM log filters bill exclusively through snapshots: the level (addressCount
// per physical filter) rises when the filter appears in the next snapshot and
// is released by its absence.
func TestLogTriggerMetering_NoRecords(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Times(3)
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Times(3)
	evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil)
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

	meta := capabilities.RequestMetadata{WorkflowID: "wf-id", WorkflowOwner: "0xOwner"}
	_, err := service.RegisterLogTrigger(t.Context(), "trigger-A", meta, meteringTestInput())
	require.NoError(t, err)
	require.Empty(t, emitter.records, "registration must not emit meter records")

	// A second trigger sharing the identical physical filter.
	_, err = service.RegisterLogTrigger(t.Context(), "trigger-B",
		capabilities.RequestMetadata{WorkflowID: "wf-2", WorkflowOwner: "0xOther"}, meteringTestInput())
	require.NoError(t, err)
	require.Empty(t, emitter.records)

	// Unregister then re-register — the shape of an engine restart.
	require.NoError(t, service.UnregisterLogTrigger(t.Context(), "trigger-A", meta, &evmcappb.FilterLogTriggerRequest{}))
	_, err = service.RegisterLogTrigger(t.Context(), "trigger-A", meta, meteringTestInput())
	require.NoError(t, err)
	require.Empty(t, emitter.records, "restart-shaped re-registration must not emit meter records")

	require.Zero(t, emitter.emitCalls, "no metering emission of any kind outside the snapshot tick")
}

func TestLogTriggerMetering_NoEmitOnRegisterFailure(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(errors.New("mocked register failure")).Once()
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

	_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, meteringTestInput())
	require.Error(t, err)
	require.Zero(t, emitter.emitCalls, "nothing may be emitted for a failed registration")
}

// TestLogTriggerMetering_DonIDNotInitialised asserts that when the host has
// not injected a capability DON ID, snapshots are still emitted but with the
// DON dimension carrying only the node ID — the consumer workflow's DON ID is
// never substituted — and the meter's DonID surfaces ErrDonIDNotInitialised
// for callers that degrade explicitly (event labels, CRE-4409).
func TestLogTriggerMetering_DonIDNotInitialised(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	service, emitter, clock := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())
	// Host did not inject a capability DON (0).
	service.meter = triggermeter.New(logger.Test(t), resourcemanager.NewResourceManager(logger.Test(t),
		resourcemanager.ResourceManagerConfig{
			MeterRecordsEnabled:   true,
			MeterSnapshotsEnabled: true,
			Emitter:               emitter,
			SnapshotInterval:      time.Minute,
			Clock:                 clock,
		}), testDeployment, 0, meteringConfig, nil, service.snapshotRows)

	_, donErr := service.meter.DonID()
	require.ErrorIs(t, donErr, triggermeter.ErrDonIDNotInitialised)

	meta := capabilities.RequestMetadata{WorkflowID: "wf-id", WorkflowOwner: "0xOwner", WorkflowDonID: 7}
	_, err := service.RegisterLogTrigger(t.Context(), triggerID, meta, meteringTestInput())
	require.NoError(t, err)

	startMeter(t, service)
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)
	require.Eventually(t, func() bool { return len(emitter.snapshots) == 1 }, time.Second, time.Millisecond)

	require.Empty(t, emitter.snapshots[0].GetIdentity().GetDon().GetDonId(),
		"the consumer workflow's DON ID must never be substituted for the capability DON")
	require.Equal(t, "csa-pubkey-hex", emitter.snapshots[0].GetIdentity().GetDon().GetNodeId(),
		"the node dimension is preserved even without a DON ID")
}

func TestLogTriggerMetering_OrphanCleanupEmitsNothing(t *testing.T) {
	// Orphan cleanup is log-poller filter hygiene, never a metering event. A
	// lost reservation is reconciled by the resource's absence from subsequent
	// Snapshots (the liveness mechanism), not by a synthetic cleanup emission.
	t.Run("stale filter cleanup emits no metering", func(t *testing.T) {
		mockEVM := evmmock.NewEVMService(t)
		store := NewLogTriggerStore()
		service, emitter, _ := newMeteredTriggerObject(t, mockEVM, store)

		liveFilterID := service.generateFilterID("live-trigger")
		staleFilterID := service.generateFilterID("stale-trigger")
		mockEVM.On("GetFiltersNames", mock.Anything).Return([]string{liveFilterID, staleFilterID}, nil).Once()
		mockEVM.On("UnregisterLogTracking", mock.Anything, staleFilterID).Return(nil).Once()
		// mimicking there's a live trigger with the filter registered to log poller
		store.Write("live-trigger", logTriggerState{filter: filter{filterID: liveFilterID}})

		service.cleanUpStaleFilters(t.Context())

		require.Zero(t, emitter.emitCalls, "orphan cleanup must not emit any metering")
	})

	t.Run("emits nothing when cleanup unregister fails", func(t *testing.T) {
		mockEVM := evmmock.NewEVMService(t)
		service, emitter, _ := newMeteredTriggerObject(t, mockEVM, NewLogTriggerStore())

		staleFilterID := service.generateFilterID("stale-trigger")
		mockEVM.On("GetFiltersNames", mock.Anything).Return([]string{staleFilterID}, nil).Once()
		mockEVM.On("UnregisterLogTracking", mock.Anything, staleFilterID).Return(errors.New("mocked cleanup failure")).Once()

		service.cleanUpStaleFilters(t.Context())
		require.Zero(t, emitter.emitCalls, "orphan cleanup never emits metering")
	})
}

// TestLogTriggerMetering_FailOpen asserts registration succeeds and snapshot
// emission failures are swallowed when the emitter errors on every call.
func TestLogTriggerMetering_FailOpen(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	service, emitter, clock := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())
	emitter.err = errors.New("mocked emitter failure")

	_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, meteringTestInput())
	require.NoError(t, err, "a metering failure must never fail registration")

	// A snapshot tick attempts the emit and swallows the failure.
	startMeter(t, service)
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)
	require.Eventually(t, func() bool { return emitter.emitCalls >= 1 }, time.Second, time.Millisecond)
	require.Empty(t, emitter.snapshots, "failed emissions record nothing")
}

// TestPhysicalFilterID_Canonicalization proves the content hash is independent
// of the order addresses / event sigs / per-slot topic values are supplied, and
// independent of which workflow or trigger registered the filter, while staying
// sensitive to the positional topic slot.
func TestPhysicalFilterID_Canonicalization(t *testing.T) {
	addrA := evmtypes.Address(expectedAddress)
	addrB := evmtypes.Address(bytes.Repeat([]byte{0x42}, evmtypes.AddressLength))
	sig1 := evmtypes.Hash(eventSig0Example)
	sig2 := evmtypes.Hash(bytes.Repeat([]byte{0x11}, evmtypes.HashLength))
	none := []evmtypes.Hash{}

	t.Run("address order does not change the id", func(t *testing.T) {
		id1 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA, addrB}, []evmtypes.Hash{sig1}, none, none, none)
		id2 := physicalFilterID(testChainSelector, []evmtypes.Address{addrB, addrA}, []evmtypes.Hash{sig1}, none, none, none)
		require.Equal(t, id1, id2)
	})

	t.Run("event sig order does not change the id", func(t *testing.T) {
		id1 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA}, []evmtypes.Hash{sig1, sig2}, none, none, none)
		id2 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA}, []evmtypes.Hash{sig2, sig1}, none, none, none)
		require.Equal(t, id1, id2)
	})

	t.Run("topic values within a slot are order-independent", func(t *testing.T) {
		id1 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA}, []evmtypes.Hash{sig1}, []evmtypes.Hash{sig1, sig2}, none, none)
		id2 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA}, []evmtypes.Hash{sig1}, []evmtypes.Hash{sig2, sig1}, none, none)
		require.Equal(t, id1, id2)
	})

	t.Run("topic slots are positional", func(t *testing.T) {
		inSlot2 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA}, []evmtypes.Hash{sig1}, []evmtypes.Hash{sig2}, none, none)
		inSlot3 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA}, []evmtypes.Hash{sig1}, none, []evmtypes.Hash{sig2}, none)
		require.NotEqual(t, inSlot2, inSlot3, "the same value in topic2 vs topic3 is a different filter")
	})

	t.Run("different chain selector changes the id", func(t *testing.T) {
		id1 := physicalFilterID(testChainSelector, []evmtypes.Address{addrA}, []evmtypes.Hash{sig1}, none, none, none)
		id2 := physicalFilterID("999", []evmtypes.Address{addrA}, []evmtypes.Hash{sig1}, none, none, none)
		require.NotEqual(t, id1, id2)
	})

	t.Run("identical filters from different workflows/triggers share one billed resource", func(t *testing.T) {
		// physicalFilterID takes only physical criteria; workflow/trigger are not
		// inputs. Two registrations with identical criteria collide by
		// construction, so the snapshot path dedups them into ONE billed row.
		evmService := initMocks(t)
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Twice()
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Twice()
		service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

		_, err := service.RegisterLogTrigger(t.Context(), "trigger-A",
			capabilities.RequestMetadata{WorkflowID: "wf-1", WorkflowOwner: "0xOwner"}, meteringTestInput())
		require.NoError(t, err)
		_, err = service.RegisterLogTrigger(t.Context(), "trigger-B",
			capabilities.RequestMetadata{WorkflowID: "wf-2", WorkflowOwner: "0xOther"}, meteringTestInput())
		require.NoError(t, err)

		require.Empty(t, emitter.records, "no deltas, ever")
		rows := service.snapshotRows(t.Context())
		require.Len(t, rows, 1, "the shared physical filter is billed as one snapshot resource")
		require.Equal(t, expectedPhysicalFilterID(t, meteringTestInput()), rows[0].ResourceID)
	})
}

// TestLogTriggerMetering_SharedFilterLevel asserts the snapshot level of a
// physical filter shared by two triggers: one row at +addressCount while any
// holder remains, and absence once the last holder unregisters.
func TestLogTriggerMetering_SharedFilterLevel(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Twice()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Twice()
	evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil)
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

	physID := expectedPhysicalFilterID(t, meteringTestInput())

	_, err := service.RegisterLogTrigger(t.Context(), "trigger-A",
		capabilities.RequestMetadata{WorkflowID: "wf-1", WorkflowOwner: "0xOwner"}, meteringTestInput())
	require.NoError(t, err)
	_, err = service.RegisterLogTrigger(t.Context(), "trigger-B",
		capabilities.RequestMetadata{WorkflowID: "wf-2", WorkflowOwner: "0xOther"}, meteringTestInput())
	require.NoError(t, err)

	rows := service.snapshotRows(t.Context())
	require.Len(t, rows, 1, "two holders of one physical filter snapshot as one resource")
	require.Equal(t, physID, rows[0].ResourceID)
	require.Equal(t, int64(2), rows[0].Value, "the level is the filter's address count, not the holder count")

	// Releasing one of two holders keeps the level.
	require.NoError(t, service.UnregisterLogTrigger(t.Context(), "trigger-A", capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{}))
	rows = service.snapshotRows(t.Context())
	require.Len(t, rows, 1)
	require.Equal(t, int64(2), rows[0].Value)

	// Releasing the last holder drops the resource: release-by-absence.
	require.NoError(t, service.UnregisterLogTrigger(t.Context(), "trigger-B", capabilities.RequestMetadata{}, &evmcappb.FilterLogTriggerRequest{}))
	require.Empty(t, service.snapshotRows(t.Context()), "the last unregister releases the level by absence from the next snapshot")

	require.Empty(t, emitter.records, "no deltas at any point in the shared-filter lifecycle")
}

// TestLogTriggerMetering_Snapshot drives one snapshot tick and asserts one
// MeterSnapshot per active filter, each fully identified by its
// ResourceIdentity (physical resource_id) with the right value. The manager
// emits one MeterSnapshot message per resource; there is no label metadata, so
// snapshots are keyed by their physical resource_id.
func TestLogTriggerMetering_Snapshot(t *testing.T) {
	mockEVM := evmmock.NewEVMService(t)
	store := NewLogTriggerStore()
	service, emitter, clock := newMeteredTriggerObject(t, mockEVM, store)

	physA := expectedPhysicalFilterID(t, meteringTestInput())
	store.Write("trigger-A", logTriggerState{filter: filter{
		filterID:             service.generateFilterID("trigger-A"),
		physicalFilterID:     physA,
		reservedAddressCount: 2,
		donID:                "42",
	}})
	store.Write("trigger-B", logTriggerState{filter: filter{
		filterID:             service.generateFilterID("trigger-B"),
		physicalFilterID:     "physB",
		reservedAddressCount: 5,
		donID:                "42",
	}})

	startMeter(t, service)
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)

	require.Eventually(t, func() bool {
		return len(emitter.snapshots) == 2
	}, time.Second, time.Millisecond)

	require.Len(t, emitter.snapshots, 2, "one MeterSnapshot per active filter")

	byResourceID := map[string]*meteringpb.MeterSnapshot{}
	for _, s := range emitter.snapshots {
		assertBaseIdentity(t, s.GetIdentity())
		byResourceID[s.GetUtilization()[0].GetResourceId()] = s
	}

	a := byResourceID[physA]
	require.NotNil(t, a)
	require.Equal(t, "2", a.GetUtilization()[0].GetValue())
	require.Equal(t, meteringConfig.ResourceType, a.GetUtilization()[0].GetResourceType())

	b := byResourceID["physB"]
	require.NotNil(t, b)
	require.Equal(t, "5", b.GetUtilization()[0].GetValue())

	// The snapshot stream is the only metering surface: no records, ever.
	require.Empty(t, emitter.records)
}

// TestLogTriggerMetering_Snapshot_NothingActive asserts an empty store emits no
// snapshots: billing zeroes a resource out by its absence from later snapshots.
func TestLogTriggerMetering_Snapshot_NothingActive(t *testing.T) {
	mockEVM := evmmock.NewEVMService(t)
	service, emitter, clock := newMeteredTriggerObject(t, mockEVM, NewLogTriggerStore())

	startMeter(t, service)
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)

	require.Empty(t, emitter.snapshots, "an empty store emits no MeterSnapshot")
}

// TestLogTriggerMetering_NoShutdownEmissions asserts that a graceful Close
// emits NO metering at all. Process-lifecycle emissions are deleted by design:
// an active filter is released by its absence from the next snapshot, not by a
// close-time drain.
func TestLogTriggerMetering_NoShutdownEmissions(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	evmService.EXPECT().GetFiltersNames(mock.Anything).Return([]string{}, nil).Maybe()
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())
	require.NoError(t, service.Start(t.Context()))

	_, err := service.RegisterLogTrigger(t.Context(), triggerID,
		capabilities.RequestMetadata{WorkflowID: "wf", WorkflowOwner: "0xOwner"}, meteringTestInput())
	require.NoError(t, err)

	require.NoError(t, service.Close())
	require.Zero(t, emitter.emitCalls, "graceful close must emit no metering")
}

// TestLogTriggerMetering_SnapshotDedup asserts the snapshot source emits one
// entry per DISTINCT physical filter (not per trigger registration): two
// triggers sharing one physicalFilterID snapshot as a single resource.
func TestLogTriggerMetering_SnapshotDedup(t *testing.T) {
	mockEVM := evmmock.NewEVMService(t)
	store := NewLogTriggerStore()
	service, emitter, clock := newMeteredTriggerObject(t, mockEVM, store)

	physShared := expectedPhysicalFilterID(t, meteringTestInput())
	// Two triggers share one physical filter.
	store.Write("trigger-A", logTriggerState{filter: filter{
		filterID: service.generateFilterID("trigger-A"), physicalFilterID: physShared, reservedAddressCount: 2, donID: "42",
	}})
	store.Write("trigger-B", logTriggerState{filter: filter{
		filterID: service.generateFilterID("trigger-B"), physicalFilterID: physShared, reservedAddressCount: 2, donID: "42",
	}})

	startMeter(t, service)
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)

	require.Eventually(t, func() bool {
		return len(emitter.snapshots) == 1
	}, time.Second, time.Millisecond)
	require.Len(t, emitter.snapshots, 1, "two triggers sharing one physical filter snapshot once")
	require.Equal(t, physShared, emitter.snapshots[0].GetUtilization()[0].GetResourceId())
	require.Equal(t, "2", emitter.snapshots[0].GetUtilization()[0].GetValue())
}
