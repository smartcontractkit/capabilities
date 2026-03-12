package transmission_schedule_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
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
	scheduler := transmission_schedule.NewTransmissionScheduler(
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
	scheduler := transmission_schedule.NewTransmissionScheduler(
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
	scheduler := transmission_schedule.NewTransmissionScheduler(
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
