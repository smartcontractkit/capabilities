package ocr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/curve25519"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

func TestEmbeddedKeyBundle(t *testing.T) {
	t.Parallel()
	a, err := EmbeddedKeyBundle(0)
	require.NoError(t, err)
	again, err := EmbeddedKeyBundle(0)
	require.NoError(t, err)
	b, err := EmbeddedKeyBundle(1)
	require.NoError(t, err)

	assert.Equal(t, a.PublicKey(), again.PublicKey())
	assert.Equal(t, a.OffchainPublicKey(), again.OffchainPublicKey())
	assert.NotEqual(t, a.ID(), b.ID())
	assert.NotEqual(t, a.PublicKey(), b.PublicKey())
	assert.NotEqual(t, a.OffchainPublicKey(), b.OffchainPublicKey())

	rc := ocrtypes.ReportContext{ReportTimestamp: ocrtypes.ReportTimestamp{Epoch: 1, Round: 2}}
	sig, err := a.Sign(rc, ocrtypes.Report("report"))
	require.NoError(t, err)
	assert.True(t, b.Verify(a.PublicKey(), rc, ocrtypes.Report("report"), sig))
	assert.False(t, b.Verify(b.PublicKey(), rc, ocrtypes.Report("report"), sig))
	assert.False(t, b.Verify(a.PublicKey(), rc, ocrtypes.Report("other"), sig))

	ab, err := a.ConfigDiffieHellman(b.ConfigEncryptionPublicKey())
	require.NoError(t, err)
	ba, err := b.ConfigDiffieHellman(a.ConfigEncryptionPublicKey())
	require.NoError(t, err)
	assert.Equal(t, ab, ba)

	basepoint := [32]byte(curve25519.Basepoint)
	pub, err := a.ConfigDiffieHellman(basepoint)
	require.NoError(t, err)
	assert.Equal(t, [32]byte(a.ConfigEncryptionPublicKey()), pub)
}

func TestEmbeddedClient(t *testing.T) {
	t.Parallel()
	client, err := NewEmbeddedClient(2)
	require.NoError(t, err)
	want, err := EmbeddedKeyBundle(2)
	require.NoError(t, err)

	kbs, err := client.ListKeyBundles(t.Context())
	require.NoError(t, err)
	require.Len(t, kbs, 1)
	assert.Equal(t, want.ID(), kbs[0].ID)
	assert.Equal(t, EmbeddedChainType, kbs[0].ChainType)
	assert.Equal(t, want.PublicKey(), kbs[0].Onchain.PublicKey())
	assert.Equal(t, want.OffchainPublicKey(), kbs[0].Offchain.OffchainPublicKey())

	kbs, err = client.ListKeyBundles(t.Context(), EmbeddedChainType)
	require.NoError(t, err)
	assert.Len(t, kbs, 1)

	kbs, err = client.ListKeyBundles(t.Context(), "solana")
	require.NoError(t, err)
	assert.Empty(t, kbs)

	kb, err := client.GetKeyBundle(t.Context(), want.ID())
	require.NoError(t, err)
	assert.Equal(t, want.ID(), kb.ID)

	_, err = client.GetKeyBundle(t.Context(), "missing")
	require.ErrorIs(t, err, ErrKeyBundleNotFound)
}
