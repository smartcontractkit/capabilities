package chain

import (
	"encoding/hex"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKeystoreFromPrivateKey covers the account an embedded run pointed at a real
// chain sends from: the one the configured key belongs to, whether or not it was
// written with the 0x a wallet exports it with.
func TestKeystoreFromPrivateKey(t *testing.T) {
	key, err := crypto.GenerateKey()
	require.NoError(t, err)

	raw := hex.EncodeToString(crypto.FromECDSA(key))
	want := crypto.PubkeyToAddress(key.PublicKey).Hex()

	for _, written := range []string{raw, "0x" + raw, "  " + raw + "\n"} {
		ks, err := KeystoreFromPrivateKey(written)
		require.NoError(t, err)

		accounts, err := ks.Accounts(t.Context())
		require.NoError(t, err)
		assert.Equal(t, []string{want}, accounts)

		// Signed as that account, and recovered to it: a key read wrongly would still
		// produce a signature, just not this node's.
		digest := crypto.Keccak256([]byte("a transaction"))
		signature, err := ks.Sign(t.Context(), want, digest)
		require.NoError(t, err)

		recovered, err := crypto.SigToPub(digest, signature)
		require.NoError(t, err)
		assert.Equal(t, want, crypto.PubkeyToAddress(*recovered).Hex())
	}
}

func TestKeystoreFromPrivateKeyRejectsNonsense(t *testing.T) {
	_, err := KeystoreFromPrivateKey("not a key")
	require.ErrorContains(t, err, "failed to read the configured private key")
}
