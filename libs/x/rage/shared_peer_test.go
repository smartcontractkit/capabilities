package rage_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/networking"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/x/rage"
)

type fakePeerSource struct {
	pgFactory networking.PeerGroupFactory
	peerID    ragetypes.PeerID
}

func (f *fakePeerSource) PeerGroupFactory() networking.PeerGroupFactory { return f.pgFactory }
func (f *fakePeerSource) PeerID() ragetypes.PeerID                      { return f.peerID }

func TestDon2DonSharedPeer_ErrorOnNilPeerSource(t *testing.T) {
	sp := rage.NewDon2DonSharedPeer(nil, nil, logger.Test(t))
	require.Error(t, sp.Start(t.Context()))
}

func TestDon2DonSharedPeer_UpdateConnectionsByDONs(t *testing.T) {
	_, myPeerID := newKeyPair(t)
	_, peerID2 := newKeyPair(t)
	_, peerID3 := newKeyPair(t)
	_, peerID4 := newKeyPair(t)
	mockPGFactory := mockPeerGroupFactory{}
	source := &fakePeerSource{pgFactory: &mockPGFactory, peerID: myPeerID}

	sp := rage.NewDon2DonSharedPeer(source, nil, logger.Test(t))
	require.NoError(t, sp.Start(t.Context()))

	donPairs := []rage.DonPair{{
		{ID: 1, Members: []ragetypes.PeerID{myPeerID, peerID2}},
		{ID: 2, Members: []ragetypes.PeerID{peerID2, peerID3}},
	}}
	// Adding a new DON pair
	require.NoError(t, sp.UpdateConnectionsByDONs(t.Context(), donPairs, rage.StreamConfig{}))
	require.Equal(t, 1, mockPGFactory.newDonGroupCounter)
	require.Equal(t, 2, mockPGFactory.newNodeGroupCounter) // myPeer is connected to peers 2 and 3
	require.Equal(t, 0, mockPGFactory.closedGroupCounter)
	require.Equal(t, 2, mockPGFactory.newStreamCounter)

	// No changes expected when updating the same group
	require.NoError(t, sp.UpdateConnectionsByDONs(t.Context(), donPairs, rage.StreamConfig{}))
	require.Equal(t, 1, mockPGFactory.newDonGroupCounter)
	require.Equal(t, 2, mockPGFactory.newNodeGroupCounter)
	require.Equal(t, 0, mockPGFactory.closedGroupCounter)
	require.Equal(t, 2, mockPGFactory.newStreamCounter)

	// Expect a change when DON membership changes
	donPairs[0][1].Members[1] = peerID4
	require.NoError(t, sp.UpdateConnectionsByDONs(t.Context(), donPairs, rage.StreamConfig{}))
	require.Equal(t, 2, mockPGFactory.newDonGroupCounter)  // update of existing group
	require.Equal(t, 3, mockPGFactory.newNodeGroupCounter) // one new connection to peer 4
	require.Equal(t, 2, mockPGFactory.closedGroupCounter)  // close old DON group + peer 2 group
	require.Equal(t, 3, mockPGFactory.newStreamCounter)    // one new connection to peer 4

	// Expect a change when a new DON pair is added
	donPairs = append(donPairs, [2]capabilities.DON{
		{ID: 3, Members: []ragetypes.PeerID{myPeerID, peerID3}},
		{ID: 2, Members: []ragetypes.PeerID{peerID2, peerID3}},
	})
	require.NoError(t, sp.UpdateConnectionsByDONs(t.Context(), donPairs, rage.StreamConfig{}))
	require.Equal(t, 3, mockPGFactory.newDonGroupCounter)
	require.Equal(t, 4, mockPGFactory.newNodeGroupCounter) // re-create connection to peer 2
	require.Equal(t, 2, mockPGFactory.closedGroupCounter)
	require.Equal(t, 4, mockPGFactory.newStreamCounter) // re-create connection to peer 2

	require.NoError(t, sp.Close())
	require.Equal(t, 2+2+3, mockPGFactory.closedGroupCounter) // closed 2 DON groups and 3 node groups
}

type mockPeerGroupFactory struct {
	newDonGroupCounter  int // large - more than 2 members
	newNodeGroupCounter int // small - 2 members
	closedGroupCounter  int
	newStreamCounter    int
}

func (m *mockPeerGroupFactory) NewPeerGroup(
	configDigest ocr2types.ConfigDigest,
	peerIDs []string,
	bootstrappers []commontypes.BootstrapperLocator,
) (networking.PeerGroup, error) {
	if len(peerIDs) > 2 {
		m.newDonGroupCounter++
	} else {
		m.newNodeGroupCounter++
	}
	return &mockPeerGroup{groupFactory: m}, nil
}

type mockPeerGroup struct {
	groupFactory *mockPeerGroupFactory
}

func (m *mockPeerGroup) NewStream(remotePeerID string, newStreamArgs networking.NewStreamArgs) (networking.Stream, error) {
	m.groupFactory.newStreamCounter++
	return &mockStream{msgCh: make(chan []byte)}, nil
}

func (m *mockPeerGroup) Close() error {
	m.groupFactory.closedGroupCounter++
	return nil
}

type mockStream struct {
	msgCh chan []byte
}

func (m *mockStream) SendMessage(data []byte) {
	m.msgCh <- data
}

func (m *mockStream) ReceiveMessages() <-chan []byte {
	return m.msgCh
}

func (m *mockStream) Close() error {
	close(m.msgCh)
	return nil
}
