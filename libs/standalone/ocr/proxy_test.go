package ocr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/standalone"
)

func TestProxy(t *testing.T) {
	t.Run("the configured peer ID is the one reported and delegated for", func(t *testing.T) {
		peerID := mustPeerID(t, 7)

		dep := &proxyDependency{lggr: logger.Test(t), cfg: ProxyConfig{
			ProxyAddress: "127.0.0.1:50051",
			PeerID:       peerID,
		}}

		factories, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, factories.Close()) })

		// No keystore was unlocked and no database was opened to learn this: the peer ID is public,
		// and the password that would yield it stays with the process hosting the peer.
		assert.Equal(t, peerID, factories.PeerID)
		assert.Equal(t, peerID.String(), factories.OCR2Endpoint.PeerID())
		assert.Equal(t, peerID.String(), factories.OCR3_1Endpoint.PeerID())
	})

	t.Run("a peer ID is decoded from its text form", func(t *testing.T) {
		// ragetypes.PeerID unmarshals text, so the flags package binds it as a leaf and rejects a
		// malformed value when the configuration is decoded rather than when it is first used.
		var decoded ragetypes.PeerID
		require.NoError(t, decoded.UnmarshalText([]byte(mustPeerID(t, 4).String())))
		assert.Equal(t, mustPeerID(t, 4), decoded)

		require.Error(t, decoded.UnmarshalText([]byte("not-a-peer-id")))
	})

	t.Run("delegating needs both the proxy address and the peer ID", func(t *testing.T) {
		_, err := (&proxyDependency{lggr: logger.Test(t)}).Get(t.Context(), standalone.CommonConfig{})
		require.ErrorContains(t, err, "--ocr.proxy-address is required")

		_, err = (&proxyDependency{lggr: logger.Test(t), cfg: ProxyConfig{ProxyAddress: "127.0.0.1:50051"}}).
			Get(t.Context(), standalone.CommonConfig{})
		require.ErrorContains(t, err, "--ocr.peer-id is required")
	})

	t.Run("an embedded instance needs neither, deriving its identity instead", func(t *testing.T) {
		// Nothing is configured here: embedding a delegating dependency yields one with no settings
		// at all, the same form a hosted peer yields.
		dep := Proxy(logger.Test(t)).ForEmbedding(2)

		factories, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)

		assert.Equal(t, mustPeerID(t, 2), factories.PeerID)
	})
}
