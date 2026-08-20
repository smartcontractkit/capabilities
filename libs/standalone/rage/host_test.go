package rage

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

func TestHostForEmbedding(t *testing.T) {
	// A nil database dependency: the adapted instances resolve without one, which is the property
	// under test - an embedded instance neither unlocks a keystore nor stores announcements.
	template := Host(logger.Test(t), nil, "announcements")

	first := template.ForEmbedding(0, 2)
	second := template.ForEmbedding(1, 2)

	t.Run("each instance derives its own identity", func(t *testing.T) {
		ctx := t.Context()

		firstFactories, err := first.Get(ctx, standalone.CommonConfig{})
		require.NoError(t, err)
		secondFactories, err := second.Get(ctx, standalone.CommonConfig{})
		require.NoError(t, err)

		assert.Equal(t, mustPeerID(t, 0), firstFactories.PeerID)
		assert.Equal(t, mustPeerID(t, 1), secondFactories.PeerID)
		// The factories announce the same identity to libocr as the one the registry sees.
		assert.Equal(t, firstFactories.PeerID.String(), firstFactories.OCR2Endpoint.PeerID())
		assert.Equal(t, secondFactories.PeerID.String(), secondFactories.OCR3_1Endpoint.PeerID())
	})

	t.Run("hosting a peer needs an address to listen on", func(t *testing.T) {
		// The dependency as a single instance resolves it, and a fresh one since the embedded forms
		// above have already resolved and cached their factories.
		_, err := Host(logger.Test(t), nil, "announcements").Get(t.Context(), standalone.CommonConfig{})
		// Cannot be a `validate` tag: an embedded instance resolves a different dependency, which
		// needs no address at all.
		require.ErrorContains(t, err, "--ocr.listen-addresses is required")
	})
}

// mustPeerID is the peer ID instance i derives, for asserting an embedded form resolved the right
// identity.
func mustPeerID(t *testing.T, i int) ragetypes.PeerID {
	t.Helper()
	peerID, err := ocr.DeterministicPeerID(i)
	require.NoError(t, err)
	return peerID
}
