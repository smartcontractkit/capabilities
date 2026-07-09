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
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	meteringpb "github.com/smartcontractkit/chainlink-protos/metering/go"
)

const testChainSelector = "5009297550715157269"

// testBaseIdentity is the producer base identity the metering tests build their
// LogTriggerService with. It carries every coarse dimension so the tests can
// assert each one is populated on the emitted records (the host-injected
// identity contract). DONID is the capability DON; an empty DONID exercises the
// WorkflowDonID fallback.
func testBaseIdentity() resourcemanager.ResourceIdentity {
	return resourcemanager.ResourceIdentity{
		Product:         "cre",
		Tenant:          "mainline",
		NumericTenantID: "42",
		Environment:     "staging",
		Zone:            "wf-zone-a",
		Don:             &resourcemanager.DonIdentity{DonID: "42", NodeID: "csa-pubkey-hex"},
		Service:         MeteringService,
		ResourcePool:    MeteringResource,
	}
}

// fakeMeterEmitter captures MeterRecords and MeterSnapshots emitted through the
// ResourceManager. The two message types are distinguished by the entity
// attribute the emitter is called with. The manager now emits one MeterSnapshot
// per active resource (no single Snapshot envelope), so snapshots accumulates
// one message per resource.
type fakeMeterEmitter struct {
	err       error
	emitCalls int
	records   []*meteringpb.MeterRecord
	snapshots []*meteringpb.MeterSnapshot
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
	return nil
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

// newMeteredTriggerObject builds a LogTriggerService whose ResourceManager is
// enabled and wired to a fake emitter. The poll interval is stretched so the
// polling goroutine stays quiet; metering happens on the register, unregister,
// cleanup, snapshot, and close paths only.
func newMeteredTriggerObject(t *testing.T, mockEVM *evmmock.EVMService, store LogTriggerStore) (*LogTriggerService, *fakeMeterEmitter, *clockwork.FakeClock) {
	t.Helper()
	lts := createTriggerObject(t, mockEVM, store)
	lts.logTriggerPollInterval = time.Hour
	emitter := &fakeMeterEmitter{}
	clock := clockwork.NewFakeClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	lts.resourceManager = resourcemanager.NewResourceManager(logger.Test(t),
		resourcemanager.ResourceManagerConfig{
			MeterRecordsEnabled:   true,
			MeterSnapshotsEnabled: true,
			Emitter:               emitter,
			SnapshotInterval:      time.Minute,
			Clock:                 clock,
		})
	lts.baseIdentity = testBaseIdentity()
	lts.chainSelector = testChainSelector
	return lts, emitter, clock
}

// meteringTestInput is a registration request with two filter addresses, so
// tests can tell an address count apart from a hardcoded 1.
func meteringTestInput() *evmcappb.FilterLogTriggerRequest {
	return &evmcappb.FilterLogTriggerRequest{
		Addresses: [][]byte{expectedAddress, bytes.Repeat([]byte{0x42}, evmtypes.AddressLength)},
		Topics:    topicsWithEventSig0,
	}
}

// assertBaseIdentity checks the six coarse dimensions + service/resource_pool on the
// emitted record identity, proving the host-injected identity is carried.
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
	require.Equal(t, MeteringService, id.GetService())
	require.Equal(t, MeteringResource, id.GetResourcePool())
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

func TestLogTriggerMetering_ReserveOnRegister(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

	meta := capabilities.RequestMetadata{WorkflowID: "wf-id", WorkflowOwner: "0xOwner"}
	_, err := service.RegisterLogTrigger(t.Context(), triggerID, meta, meteringTestInput())
	require.NoError(t, err)

	require.Len(t, emitter.records, 1)
	record := emitter.records[0]
	assertBaseIdentity(t, record.GetIdentity())
	physID := expectedPhysicalFilterID(t, meteringTestInput())
	require.Equal(t, physID, record.GetUtilizations()[0].GetResourceId(), "resource_id must be the physical filter content hash")
	require.Equal(t, meteringpb.MeterAction_METER_ACTION_RESERVE, record.GetAction())
	require.Len(t, record.GetUtilizations(), 1)
	require.Equal(t, "2", record.GetUtilizations()[0].GetValue(), "RESERVE value must equal the filter address count")
	require.Equal(t, MeteringResourceType, record.GetUtilizations()[0].GetResourceType())
}

func TestLogTriggerMetering_DonIDFallbackToWorkflowDon(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())
	// Host did not inject a capability DON; the consumer's WorkflowDonID is the
	// documented fallback resolved at emit time.
	service.baseIdentity.Don = &resourcemanager.DonIdentity{NodeID: "csa-pubkey-hex"}

	meta := capabilities.RequestMetadata{WorkflowID: "wf-id", WorkflowOwner: "0xOwner", WorkflowDonID: 7}
	_, err := service.RegisterLogTrigger(t.Context(), triggerID, meta, meteringTestInput())
	require.NoError(t, err)

	require.Len(t, emitter.records, 1)
	require.Equal(t, "7", emitter.records[0].GetIdentity().GetDon().GetDonId(), "empty capability DON must fall back to WorkflowDonID")
}

func TestLogTriggerMetering_NoReserveOnRegisterFailure(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(errors.New("mocked register failure")).Once()
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

	_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, meteringTestInput())
	require.Error(t, err)
	require.Zero(t, emitter.emitCalls, "no RESERVE may be emitted for a failed registration")
}

func TestLogTriggerMetering_ReleaseOnUnregister(t *testing.T) {
	meta := capabilities.RequestMetadata{WorkflowID: "wf-id", WorkflowOwner: "0xOwner"}

	registerTrigger := func(t *testing.T, service *LogTriggerService) {
		t.Helper()
		_, err := service.RegisterLogTrigger(t.Context(), triggerID, meta, meteringTestInput())
		require.NoError(t, err)
		// The trigger state (holding the reserved address count) is written by
		// the polling goroutine; wait for it before unregistering.
		require.Eventually(t, func() bool {
			_, ok := service.triggers.Read(triggerID)
			return ok
		}, time.Second, time.Millisecond)
	}

	assertRelease := func(t *testing.T, service *LogTriggerService, record *meteringpb.MeterRecord) {
		t.Helper()
		assertBaseIdentity(t, record.GetIdentity())
		require.Equal(t, meteringpb.MeterAction_METER_ACTION_RELEASE, record.GetAction())
		require.Len(t, record.GetUtilizations(), 1)
		require.Equal(t, "2", record.GetUtilizations()[0].GetValue(), "RELEASE must carry the same value that was reserved")
		physID := expectedPhysicalFilterID(t, meteringTestInput())
		require.Equal(t, physID, record.GetUtilizations()[0].GetResourceId())
	}

	t.Run("release pairs the reserve", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
		evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
		service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

		registerTrigger(t, service)
		require.NoError(t, service.UnregisterLogTrigger(t.Context(), triggerID, meta, &evmcappb.FilterLogTriggerRequest{}))

		require.Len(t, emitter.records, 2)
		require.Equal(t, meteringpb.MeterAction_METER_ACTION_RESERVE, emitter.records[0].GetAction())
		assertRelease(t, service, emitter.records[1])
		require.Equal(t, emitter.records[0].GetUtilizations()[0].GetValue(), emitter.records[1].GetUtilizations()[0].GetValue())
		require.Equal(t, emitter.records[0].GetUtilizations()[0].GetResourceId(), emitter.records[1].GetUtilizations()[0].GetResourceId(),
			"RESERVE and RELEASE must share one physical resource_id")
	})

	t.Run("release emitted even when UnregisterLogTracking fails", func(t *testing.T) {
		evmService := initMocks(t)
		evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
		evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
		evmService.On("UnregisterLogTracking", mock.Anything, mock.Anything).Return(errors.New("mocked unregister failure")).Once()
		service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())

		registerTrigger(t, service)
		// The reservation is released here (from the stashed count) before the
		// UnregisterLogTracking RPC. If the RPC fails the filter is orphaned at
		// the log poller; the cleanup thread unregisters it silently, emitting
		// no further metering record.
		require.Error(t, service.UnregisterLogTrigger(t.Context(), triggerID, meta, &evmcappb.FilterLogTriggerRequest{}))

		require.Len(t, emitter.records, 2)
		assertRelease(t, service, emitter.records[1])
	})
}

func TestLogTriggerMetering_OrphanCleanupEmitsNothing(t *testing.T) {
	// Orphan cleanup is log-poller filter hygiene, never a metering event. A
	// lost reservation is reconciled by the resource's absence from subsequent
	// Snapshots (the liveness mechanism), not by a synthetic cleanup RELEASE.
	t.Run("stale filter cleanup emits no meter record", func(t *testing.T) {
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

		require.Zero(t, emitter.emitCalls, "orphan cleanup must not emit any MeterRecord")
	})

	t.Run("emits nothing when cleanup unregister fails", func(t *testing.T) {
		mockEVM := evmmock.NewEVMService(t)
		service, emitter, _ := newMeteredTriggerObject(t, mockEVM, NewLogTriggerStore())

		staleFilterID := service.generateFilterID("stale-trigger")
		mockEVM.On("GetFiltersNames", mock.Anything).Return([]string{staleFilterID}, nil).Once()
		mockEVM.On("UnregisterLogTracking", mock.Anything, staleFilterID).Return(errors.New("mocked cleanup failure")).Once()

		service.cleanUpStaleFilters(t.Context())
		require.Zero(t, emitter.emitCalls, "orphan cleanup never emits a meter record")
	})
}

func TestLogTriggerMetering_FailOpen(t *testing.T) {
	evmService := initMocks(t)
	evmService.EXPECT().GetLatestLPBlock(mock.Anything).Return(&finalizedExpBlock, nil).Once()
	evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil).Once()
	service, emitter, _ := newMeteredTriggerObject(t, evmService, NewLogTriggerStore())
	emitter.err = errors.New("mocked emitter failure")

	_, err := service.RegisterLogTrigger(t.Context(), triggerID, capabilities.RequestMetadata{WorkflowID: "wf-id"}, meteringTestInput())
	require.NoError(t, err, "a metering emit failure must never fail registration")
	require.Equal(t, 1, emitter.emitCalls, "the emit was attempted and its failure swallowed")
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

	t.Run("identical filters from different workflows/triggers share one id", func(t *testing.T) {
		// physicalFilterID takes only physical criteria; workflow/trigger are not
		// inputs. Two registrations with identical criteria therefore collide by
		// construction, which this asserts end to end through the register path.
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

		require.Len(t, emitter.records, 2)
		require.Equal(t,
			emitter.records[0].GetUtilizations()[0].GetResourceId(),
			emitter.records[1].GetUtilizations()[0].GetResourceId(),
			"identical physical filters must share one resource_id across workflows/triggers")
	})
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

	unregister := service.resourceManager.Register(service)
	t.Cleanup(unregister)
	servicetest.Run(t, service.resourceManager)
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
	require.Equal(t, MeteringResourceType, a.GetUtilization()[0].GetResourceType())

	b := byResourceID["physB"]
	require.NotNil(t, b)
	require.Equal(t, "5", b.GetUtilization()[0].GetValue())
}

// TestLogTriggerMetering_Snapshot_NothingActive asserts an empty store emits no
// snapshots: billing zeroes a resource out by its absence from later snapshots.
func TestLogTriggerMetering_Snapshot_NothingActive(t *testing.T) {
	mockEVM := evmmock.NewEVMService(t)
	service, emitter, clock := newMeteredTriggerObject(t, mockEVM, NewLogTriggerStore())

	unregister := service.resourceManager.Register(service)
	t.Cleanup(unregister)
	servicetest.Run(t, service.resourceManager)
	require.NoError(t, clock.BlockUntilContext(t.Context(), 1))
	clock.Advance(time.Minute)

	require.Empty(t, emitter.snapshots, "an empty store emits no MeterSnapshot")
}

// TestLogTriggerMetering_ReleaseOnGracefulClose asserts that closing the service
// releases every still-active filter so a clean shutdown is not seen as a leak.
func TestLogTriggerMetering_ReleaseOnGracefulClose(t *testing.T) {
	mockEVM := evmmock.NewEVMService(t)
	store := NewLogTriggerStore()
	service, emitter, _ := newMeteredTriggerObject(t, mockEVM, store)

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

	service.releaseActiveFiltersOnClose(t.Context())

	require.Len(t, emitter.records, 2, "one RELEASE per active filter on graceful close")
	for _, record := range emitter.records {
		require.Equal(t, meteringpb.MeterAction_METER_ACTION_RELEASE, record.GetAction())
		assertBaseIdentity(t, record.GetIdentity())
	}
	// The records carry no label metadata, so pair them by their physical
	// resource_id (the only per-filter discriminator on the record).
	byResourceID := map[string]*meteringpb.MeterRecord{}
	for _, record := range emitter.records {
		byResourceID[record.GetUtilizations()[0].GetResourceId()] = record
	}
	require.Equal(t, "2", byResourceID[physA].GetUtilizations()[0].GetValue())
	require.Equal(t, "5", byResourceID["physB"].GetUtilizations()[0].GetValue())
}
