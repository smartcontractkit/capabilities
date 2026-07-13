package transmissionschedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
)

func peerID(seed byte) p2ptypes.PeerID {
	var id p2ptypes.PeerID
	id[0] = seed
	return id
}

func TestTransmissionScheduler_GetQueuePosition_SingleNode(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	myPeerID := peerID(0x01)
	scheduler := ts.NewTransmissionScheduler(
		myPeerID,
		[]p2ptypes.PeerID{myPeerID},
		10*time.Millisecond,
		1,
		lggr,
	)

	pos := scheduler.GetQueuePosition("tx-1")
	require.Equal(t, 0, pos)
}

func TestTransmissionScheduler_GetQueuePosition_NodeNotInDON(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	myPeerID := peerID(0x01)
	scheduler := ts.NewTransmissionScheduler(
		myPeerID,
		[]p2ptypes.PeerID{peerID(0x02)},
		10*time.Millisecond,
		1,
		lggr,
	)

	pos := scheduler.GetQueuePosition("tx-1")
	require.Equal(t, -1, pos)
}

func TestTransmissionScheduler_GetQueuePosition_Deterministic(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	myPeerID := peerID(0x02)
	scheduler := ts.NewTransmissionScheduler(
		myPeerID,
		[]p2ptypes.PeerID{peerID(0x01), myPeerID, peerID(0x03)},
		10*time.Millisecond,
		1,
		lggr,
	)

	pos1 := scheduler.GetQueuePosition("tx-1")
	pos2 := scheduler.GetQueuePosition("tx-1")
	require.Equal(t, pos1, pos2)
	require.GreaterOrEqual(t, pos1, 0)
	require.Less(t, pos1, 3)
}

func TestInitialiseTransmissionScheduler_DuplicatePeerIDs(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	myPeerID := peerID(0x01)
	dup := peerID(0x02)

	reg := mocks.NewCapabilitiesRegistry(t)
	reg.EXPECT().LocalNode(context.Background()).Return(capabilities.Node{PeerID: &myPeerID}, nil)

	don := &capabilities.DON{
		Members: []p2ptypes.PeerID{myPeerID, dup, dup},
		F:       1,
	}

	_, err := ts.InitialiseTransmissionScheduler(context.Background(), reg, 10*time.Millisecond, lggr, don, false)
	require.ErrorContains(t, err, "duplicate peer ID")
}

func TestInitialiseTransmissionScheduler_UniquePeerIDs(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	myPeerID := peerID(0x01)

	reg := mocks.NewCapabilitiesRegistry(t)
	reg.EXPECT().LocalNode(context.Background()).Return(capabilities.Node{PeerID: &myPeerID}, nil)

	don := &capabilities.DON{
		Members: []p2ptypes.PeerID{myPeerID, peerID(0x02), peerID(0x03)},
		F:       1,
	}

	_, err := ts.InitialiseTransmissionScheduler(context.Background(), reg, 10*time.Millisecond, lggr, don, false)
	require.NoError(t, err)
}

func TestNewTransmissionScheduler_FallsBackToDefaultDeltaStage(t *testing.T) {
	t.Parallel()

	lggr := logger.Test(t)
	myPeerID := peerID(0x01)

	t.Run("zero deltaStage uses default", func(t *testing.T) {
		s := ts.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{myPeerID}, 0, 1, lggr)
		require.Equal(t, ts.DefaultDeltaStage, s.DeltaStage)
	})

	t.Run("negative deltaStage uses default", func(t *testing.T) {
		s := ts.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{myPeerID}, -1*time.Second, 1, lggr)
		require.Equal(t, ts.DefaultDeltaStage, s.DeltaStage)
	})

	t.Run("positive deltaStage preserved", func(t *testing.T) {
		want := 7 * time.Second
		s := ts.NewTransmissionScheduler(myPeerID, []p2ptypes.PeerID{myPeerID}, want, 1, lggr)
		require.Equal(t, want, s.DeltaStage)
	})
}

func TestInitMyDON(t *testing.T) {
	t.Parallel()

	const capID = "evm:ChainSelector:42@1.0.0"
	myPeerID := peerID(0x01)
	otherPeerID := peerID(0x02)

	// buildDONs builds a DONsForCapability response from a map of donID -> member peer IDs.
	buildDONs := func(dons map[uint32][]p2ptypes.PeerID) []capabilities.DONWithNodes {
		var dwns []capabilities.DONWithNodes
		for id, members := range dons {
			d := capabilities.DON{ID: id, Members: members}
			nodes := make([]capabilities.Node, 0, len(members))
			for _, m := range members {
				nodes = append(nodes, capabilities.Node{PeerID: &m})
			}
			dwns = append(dwns, capabilities.DONWithNodes{DON: d, Nodes: nodes})
		}
		return dwns
	}

	// withLocalNode wires LocalNode on the mock (only needed for legacy path tests).
	withLocalNode := func(reg *mocks.CapabilitiesRegistry) {
		reg.EXPECT().LocalNode(mock.Anything).Return(capabilities.Node{PeerID: &myPeerID}, nil).Maybe()
	}

	t.Run("isLocal returns empty DON", func(t *testing.T) {
		got, err := ts.InitMyDON(context.Background(), nil, capID, 0, logger.Test(t), true)
		require.NoError(t, err)
		require.Equal(t, capabilities.DON{}, got)
	})

	// --- authoritative ID path (post-CRE-4409) ---

	t.Run("authoritative: returns correct DON by ID", func(t *testing.T) {
		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().DONsForCapability(mock.Anything, capID).Return(buildDONs(map[uint32][]p2ptypes.PeerID{
			10: {myPeerID, otherPeerID},
			20: {myPeerID, otherPeerID},
		}), nil)

		got, err := ts.InitMyDON(context.Background(), reg, capID, 20, logger.Test(t), false)
		require.NoError(t, err)
		require.EqualValues(t, 20, got.ID)
	})

	t.Run("authoritative: selects correct DON even when local node is not listed as a member of all DONs", func(t *testing.T) {
		// Plex scenario: node serves DON 20 but the registry lists it only in DON 10's
		// membership. With the authoritative ID we bypass peer-membership filtering
		// entirely and return DON 20 directly.
		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().DONsForCapability(mock.Anything, capID).Return(buildDONs(map[uint32][]p2ptypes.PeerID{
			10: {myPeerID},
			20: {otherPeerID},
		}), nil)

		got, err := ts.InitMyDON(context.Background(), reg, capID, 20, logger.Test(t), false)
		require.NoError(t, err)
		require.EqualValues(t, 20, got.ID)
	})

	t.Run("authoritative: error when DON ID not found in registry", func(t *testing.T) {
		reg := mocks.NewCapabilitiesRegistry(t)
		reg.EXPECT().DONsForCapability(mock.Anything, capID).Return(buildDONs(map[uint32][]p2ptypes.PeerID{
			10: {myPeerID},
			20: {myPeerID},
		}), nil)

		_, err := ts.InitMyDON(context.Background(), reg, capID, 99, logger.Test(t), false)
		require.ErrorContains(t, err, "authoritative DON ID 99 not found")
	})

	// --- legacy path (authoritativeDonID == 0) ---

	t.Run("legacy: no DON matches local peer", func(t *testing.T) {
		reg := mocks.NewCapabilitiesRegistry(t)
		withLocalNode(reg)
		reg.EXPECT().DONsForCapability(mock.Anything, capID).Return(buildDONs(map[uint32][]p2ptypes.PeerID{
			10: {otherPeerID},
		}), nil)

		_, err := ts.InitMyDON(context.Background(), reg, capID, 0, logger.Test(t), false)
		require.ErrorContains(t, err, "failed to find don for my peer ID")
	})

	t.Run("legacy: single matched DON returns that DON", func(t *testing.T) {
		reg := mocks.NewCapabilitiesRegistry(t)
		withLocalNode(reg)
		reg.EXPECT().DONsForCapability(mock.Anything, capID).Return(buildDONs(map[uint32][]p2ptypes.PeerID{
			10: {myPeerID, otherPeerID},
		}), nil)

		got, err := ts.InitMyDON(context.Background(), reg, capID, 0, logger.Test(t), false)
		require.NoError(t, err)
		require.EqualValues(t, 10, got.ID)
	})

	t.Run("legacy: multiple matched DONs returns first and warns", func(t *testing.T) {
		reg := mocks.NewCapabilitiesRegistry(t)
		withLocalNode(reg)
		reg.EXPECT().DONsForCapability(mock.Anything, capID).Return(buildDONs(map[uint32][]p2ptypes.PeerID{
			10: {myPeerID},
			20: {myPeerID},
		}), nil)

		got, err := ts.InitMyDON(context.Background(), reg, capID, 0, logger.Test(t), false)
		require.NoError(t, err)
		require.Contains(t, []uint32{10, 20}, got.ID, "returns one of the matched DONs")
	})
}
