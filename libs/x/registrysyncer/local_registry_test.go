package registrysyncer

import (
	"context"
	"errors"
	"testing"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func peer(b byte) ragetypes.PeerID {
	var p ragetypes.PeerID
	p[0] = b
	return p
}

// testLocalRegistry builds a snapshot with:
//   - DON 1: workflow DON (accepts workflows), members p1, p2
//   - DON 2: capability DON hosting act@1.0.0, members p1, p3
func testLocalRegistry(t *testing.T, self ragetypes.PeerID) *LocalRegistry {
	t.Helper()

	p1, p2, p3 := peer(1), peer(2), peer(3)

	return NewLocalRegistry(
		logger.Test(t),
		func() (ragetypes.PeerID, error) { return self, nil },
		map[DonID]DON{
			1: {
				DON: capabilities.DON{
					ID: 1, Name: "wf-don", F: 1, ConfigVersion: 3,
					Members:          []ragetypes.PeerID{p1, p2},
					Families:         []string{"zone-a"},
					AcceptsWorkflows: true,
				},
				CapabilityConfigurations: map[string]CapabilityConfiguration{},
			},
			2: {
				DON: capabilities.DON{
					ID: 2, Name: "cap-don", F: 2, ConfigVersion: 5,
					Members: []ragetypes.PeerID{p1, p3},
				},
				CapabilityConfigurations: map[string]CapabilityConfiguration{"act@1.0.0": {Config: []byte("cfg-bytes")}},
			},
		},
		map[ragetypes.PeerID]NodeInfo{
			p1: {NodeOperatorID: 11, P2pID: p1, Signer: [32]byte{0xaa}, WorkflowDONID: 1},
			p2: {NodeOperatorID: 12, P2pID: p2, WorkflowDONID: 1},
			p3: {NodeOperatorID: 13, P2pID: p3},
		},
		map[string]Capability{
			"act@1.0.0": {ID: "act@1.0.0", CapabilityType: capabilities.CapabilityTypeAction},
		},
	)
}

func TestLocalRegistry_NodeByPeerIDSplitsWorkflowAndCapabilityDONs(t *testing.T) {
	ctx := context.Background()
	lr := testLocalRegistry(t, peer(1))

	// p1 is in both DONs: the workflow DON is the one that accepts workflows,
	// and every DON it belongs to shows up under CapabilityDONs.
	got, err := lr.NodeByPeerID(ctx, peer(1))
	require.NoError(t, err)
	assert.Equal(t, uint32(11), got.NodeOperatorID)
	assert.Equal(t, [32]byte{0xaa}, got.Signer)
	assert.Equal(t, uint32(1), got.WorkflowDON.ID)
	assert.Len(t, got.CapabilityDONs, 2)

	// p3 is only in the capability DON, so it has no workflow DON at all.
	got3, err := lr.NodeByPeerID(ctx, peer(3))
	require.NoError(t, err)
	assert.Zero(t, got3.WorkflowDON.ID)
	require.Len(t, got3.CapabilityDONs, 1)
	assert.Equal(t, uint32(2), got3.CapabilityDONs[0].ID)
}

func TestLocalRegistry_NodeByPeerIDUnknownPeer(t *testing.T) {
	ctx := context.Background()
	lr := testLocalRegistry(t, peer(1))

	_, err := lr.NodeByPeerID(ctx, peer(99))
	require.Error(t, err)
}

func TestLocalRegistry_LocalNodeUsesGetPeerID(t *testing.T) {
	ctx := context.Background()

	lr := testLocalRegistry(t, peer(3))
	got, err := lr.LocalNode(ctx)
	require.NoError(t, err)
	require.NotNil(t, got.PeerID)
	assert.Equal(t, peer(3), *got.PeerID)
	assert.Equal(t, uint32(13), got.NodeOperatorID)
}

func TestLocalRegistry_LocalNodeCaches(t *testing.T) {
	ctx := context.Background()

	calls := 0
	lr := testLocalRegistry(t, peer(1))
	lr.GetPeerID = func() (ragetypes.PeerID, error) {
		calls++
		return peer(1), nil
	}

	for i := 0; i < 3; i++ {
		_, err := lr.LocalNode(ctx)
		require.NoError(t, err)
	}

	// GetPeerID is consulted every time (the cache is keyed on it), but the
	// derived node is only built once. The cache is per-snapshot, so a new sync
	// still yields fresh DON data.
	assert.Equal(t, 3, calls)
}

func TestLocalRegistry_LocalNodePeerIDError(t *testing.T) {
	ctx := context.Background()

	lr := testLocalRegistry(t, peer(1))
	lr.GetPeerID = func() (ragetypes.PeerID, error) { return ragetypes.PeerID{}, errors.New("keystore locked") }

	_, err := lr.LocalNode(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "keystore locked")
}

func TestLocalRegistry_DONsForCapability(t *testing.T) {
	ctx := context.Background()
	lr := testLocalRegistry(t, peer(1))

	got, err := lr.DONsForCapability(ctx, "act@1.0.0")
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, uint32(2), got[0].DON.ID)

	// Nodes must be populated, not left empty: callers read signers off them.
	require.Len(t, got[0].Nodes, 2)
	ops := []uint32{got[0].Nodes[0].NodeOperatorID, got[0].Nodes[1].NodeOperatorID}
	assert.ElementsMatch(t, []uint32{11, 13}, ops)
}

func TestLocalRegistry_DONsForCapabilityUnknown(t *testing.T) {
	ctx := context.Background()
	lr := testLocalRegistry(t, peer(1))

	_, err := lr.DONsForCapability(ctx, "nope@1.0.0")
	require.Error(t, err)
}

func TestLocalRegistry_DONByIDResolvesWorkflowDON(t *testing.T) {
	ctx := context.Background()
	lr := testLocalRegistry(t, peer(1))

	// DON 1 hosts no capability, so DONsForCapability would never surface it.
	// DONByID must still resolve it, because callers read a caller DON's Families
	// (zone membership) from its ID.
	got, err := lr.DONByID(ctx, 1)
	require.NoError(t, err)
	assert.Equal(t, "wf-don", got.Name)
	assert.Equal(t, []string{"zone-a"}, got.Families)

	_, err = lr.DONByID(ctx, 404)
	require.Error(t, err)
}

func TestLocalRegistry_RawConfigForCapability(t *testing.T) {
	ctx := context.Background()
	lr := testLocalRegistry(t, peer(1))

	got, err := lr.RawConfigForCapability(ctx, "act@1.0.0", 2)
	require.NoError(t, err)
	assert.Equal(t, []byte("cfg-bytes"), got)

	// Wrong DON, and unknown capability on a known DON, are both errors.
	_, err = lr.RawConfigForCapability(ctx, "act@1.0.0", 1)
	require.Error(t, err)

	_, err = lr.RawConfigForCapability(ctx, "nope@1.0.0", 2)
	require.Error(t, err)
}

func TestLocalRegistry_EmptySnapshotIsRejected(t *testing.T) {
	ctx := context.Background()

	// An empty snapshot means the sync has not produced usable data. Answering
	// from it would look like "this node belongs to no DON", which reads as a
	// deliberate configuration rather than a missing read.
	lr := NewLocalRegistry(
		logger.Test(t),
		func() (ragetypes.PeerID, error) { return peer(1), nil },
		map[DonID]DON{},
		map[ragetypes.PeerID]NodeInfo{},
		map[string]Capability{},
	)

	_, err := lr.NodeByPeerID(ctx, peer(1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty local registry")

	_, err = lr.DONByID(ctx, 1)
	require.Error(t, err)

	_, err = lr.DONsForCapability(ctx, "act@1.0.0")
	require.Error(t, err)

	_, err = lr.RawConfigForCapability(ctx, "act@1.0.0", 1)
	require.Error(t, err)
}
