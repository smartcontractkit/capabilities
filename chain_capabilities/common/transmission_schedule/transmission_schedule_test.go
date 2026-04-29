package transmissionschedule_test

import (
	"context"
	"testing"
	"time"

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
