package capability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedPeerIDsDiffer(t *testing.T) {
	t.Parallel()
	first, err := EmbeddedPeerID(0)
	require.NoError(t, err)
	again, err := EmbeddedPeerID(0)
	require.NoError(t, err)
	second, err := EmbeddedPeerID(1)
	require.NoError(t, err)

	assert.Equal(t, first, again, "an instance's peer is derived from its index")
	assert.NotEqual(t, first, second)
}

func TestLocalMetadata(t *testing.T) {
	t.Parallel()
	m, err := newLocalMetadata(InstanceInfo{ID: 2, Total: 4}, 7)
	require.NoError(t, err)

	local, err := m.LocalNode(t.Context())
	require.NoError(t, err)
	peerID, err := EmbeddedPeerID(2)
	require.NoError(t, err)
	assert.Equal(t, peerID, *local.PeerID)
	assert.NotEqual(t, [32]byte{}, local.Signer, "the signer is the instance's evm onchain key")
	assert.Equal(t, uint32(7), local.WorkflowDON.ID, "the run's DON also runs workflows")
	assert.Len(t, local.WorkflowDON.Members, 4)
	assert.Equal(t, uint8(1), local.WorkflowDON.F)
	require.Len(t, local.CapabilityDONs, 1)
	assert.Equal(t, uint32(7), local.CapabilityDONs[0].ID)

	other, err := EmbeddedPeerID(0)
	require.NoError(t, err)
	node, err := m.NodeByPeerID(t.Context(), other)
	require.NoError(t, err)
	assert.Equal(t, other, *node.PeerID)
	assert.NotEqual(t, local.Signer, node.Signer)

	dons, err := m.DONsForCapability(t.Context(), fakeID)
	require.NoError(t, err)
	require.Len(t, dons, 1)
	assert.Equal(t, local.WorkflowDON, dons[0].DON)
	assert.Len(t, dons[0].Nodes, 4)

	_, err = m.ConfigForCapability(t.Context(), fakeID, 7)
	require.NoError(t, err)

	_, err = m.ConfigForCapability(t.Context(), fakeID, 8)
	require.Error(t, err, "there is no other DON")
	_, err = m.DONByID(t.Context(), 8)
	require.Error(t, err)
}
