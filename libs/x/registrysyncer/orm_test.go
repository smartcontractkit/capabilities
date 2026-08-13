package registrysyncer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// A stored snapshot is what a restart answers from until its first read lands, so everything the
// lookups need has to survive the round trip.
func TestLocalRegistry_JSONRoundTrip(t *testing.T) {
	original := testLocalRegistry(t, peer(1))

	encoded, err := original.MarshalJSON()
	require.NoError(t, err)

	var restored LocalRegistry
	require.NoError(t, restored.UnmarshalJSON(encoded))

	assert.Equal(t, original.IDsToDONs, restored.IDsToDONs)
	assert.Equal(t, original.IDsToNodes, restored.IDsToNodes)
	assert.Equal(t, original.IDsToCapabilities, restored.IDsToCapabilities)

	// Capability config is what a caller ultimately asks for, and it is stored as raw bytes, so it
	// has to come back byte for byte rather than merely present.
	raw, err := restored.RawConfigForCapability(t.Context(), "act@1.0.0", 2)
	require.NoError(t, err)
	assert.Equal(t, []byte("cfg-bytes"), raw)
}

// Neither of these survives JSON, so a restored snapshot cannot resolve the local node until the
// caller puts them back - which is the one thing a restore has to remember to do.
func TestLocalRegistry_RestoredSnapshotNeedsItsLoggerAndIdentityBack(t *testing.T) {
	encoded, err := testLocalRegistry(t, peer(1)).MarshalJSON()
	require.NoError(t, err)

	var restored LocalRegistry
	require.NoError(t, restored.UnmarshalJSON(encoded))
	require.Nil(t, restored.GetPeerID)

	restored.Logger = logger.Test(t)
	restored.GetPeerID = func() (ragetypes.PeerID, error) { return peer(1), nil }

	node, err := restored.LocalNode(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint32(11), node.NodeOperatorID)
	assert.Equal(t, uint32(1), node.WorkflowDON.ID)
}

func TestNewORM_RejectsATableNameItWouldHaveToInterpolate(t *testing.T) {
	// The name goes into the SQL text, where binding cannot protect it, so anything that is not a
	// plain identifier is refused rather than escaped.
	assert.Panics(t, func() { NewORM(nil, logger.Test(t), "snapshots; DROP TABLE users--") })
	assert.Panics(t, func() { NewORM(nil, logger.Test(t), "") })
	assert.NotPanics(t, func() { NewORM(nil, logger.Test(t), "proxy_registry_snapshots") })
}

func TestFromSnapshot_WrapsConfigsWithoutDecodingThem(t *testing.T) {
	self := peer(1)
	lr := FromSnapshot(logger.Test(t), func() (ragetypes.PeerID, error) { return self, nil }, &Snapshot{
		DONs: map[DonID]DON{
			2: {
				DON:                      capabilities.DON{ID: 2, Members: []ragetypes.PeerID{self}},
				CapabilityConfigurations: map[string]CapabilityConfiguration{"act@1.0.0": {Config: []byte("not a proto")}},
			},
		},
		Nodes:        map[ragetypes.PeerID]NodeInfo{self: {P2pID: self}},
		Capabilities: map[string]Capability{"act@1.0.0": {ID: "act@1.0.0"}},
	})

	// Undecodable bytes are carried without complaint, because nothing decoded them on the way in.
	raw, err := lr.RawConfigForCapability(t.Context(), "act@1.0.0", 2)
	require.NoError(t, err)
	assert.Equal(t, []byte("not a proto"), raw)

	// They only fail for the caller that actually asks for the decoded form.
	_, err = lr.ConfigForCapability(t.Context(), "act@1.0.0", 2)
	require.Error(t, err)
}
