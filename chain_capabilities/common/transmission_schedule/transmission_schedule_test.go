package transmissionschedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

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

type mockRegistry struct {
	core.UnimplementedCapabilitiesRegistry
	localPeerID p2ptypes.PeerID
	dons        []capabilities.DONWithNodes
}

func (r *mockRegistry) LocalNode(_ context.Context) (capabilities.Node, error) {
	id := r.localPeerID
	return capabilities.Node{PeerID: &id}, nil
}

func (r *mockRegistry) DONsForCapability(_ context.Context, _ string) ([]capabilities.DONWithNodes, error) {
	return r.dons, nil
}

func newDONContainingPeer(id uint32, name string, local p2ptypes.PeerID) capabilities.DONWithNodes {
	return capabilities.DONWithNodes{
		DON:   capabilities.DON{ID: id, Name: name, Members: []p2ptypes.PeerID{local}},
		Nodes: []capabilities.Node{{PeerID: &local}},
	}
}

func TestInitMyDON_MultipleMatches_Errors(t *testing.T) {
	t.Parallel()

	local := peerID(0xAA)
	reg := &mockRegistry{
		localPeerID: local,
		dons: []capabilities.DONWithNodes{
			newDONContainingPeer(1, "don-a", local),
			newDONContainingPeer(2, "don-b", local),
		},
	}

	_, err := ts.InitMyDON(t.Context(), reg, "cap-1", logger.Test(t), false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "multiple DONs")
	require.Contains(t, err.Error(), "1 (don-a)")
	require.Contains(t, err.Error(), "2 (don-b)")
}
