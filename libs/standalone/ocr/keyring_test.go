package ocr

import (
	"crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"
)

func TestDeterministicKeyring(t *testing.T) {
	t.Run("an instance's identity is the same every time it is derived", func(t *testing.T) {
		first, err := DeterministicPeerID(3)
		require.NoError(t, err)
		again, err := DeterministicPeerID(3)
		require.NoError(t, err)

		// The whole point: a caller can name the members of an embedded DON - in an OCR config, a
		// registry entry, a test expectation - before the instances exist, and get the same answer
		// on the next run and on another machine.
		assert.Equal(t, first, again)
	})

	t.Run("instances have different identities", func(t *testing.T) {
		seen := map[ragetypes.PeerID]int{}
		for i := range 8 {
			peerID, err := DeterministicPeerID(i)
			require.NoError(t, err)
			if previous, exists := seen[peerID]; exists {
				t.Fatalf("instances %d and %d derived the same peer ID %s", previous, i, peerID)
			}
			seen[peerID] = i
		}
	})

	t.Run("the derived key signs as the peer ID it announces", func(t *testing.T) {
		keyring, err := DeterministicKeyring(1)
		require.NoError(t, err)

		msg := []byte("signed by instance 1")
		sig, err := keyring.Sign(msg)
		require.NoError(t, err)

		pub := keyring.PublicKey()
		assert.True(t, ed25519.Verify(pub[:], msg, sig),
			"signature does not verify against the keyring's own public key")
		assert.Equal(t, ragetypes.PeerIDFromKeyring(keyring), mustPeerID(t, 1))
	})
}

func mustPeerID(t *testing.T, i int) ragetypes.PeerID {
	t.Helper()
	peerID, err := DeterministicPeerID(i)
	require.NoError(t, err)
	return peerID
}
