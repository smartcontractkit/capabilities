package ocr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
)

func TestEmbeddedKeyBundles(t *testing.T) {
	t.Parallel()
	a, err := EmbeddedKeyBundles(0)
	require.NoError(t, err)
	again, err := EmbeddedKeyBundles(0)
	require.NoError(t, err)
	b, err := EmbeddedKeyBundles(1)
	require.NoError(t, err)

	require.Len(t, a, len(corekeys.SupportedChainTypes), "one bundle per chain type")
	for j, chainType := range corekeys.SupportedChainTypes {
		assert.Equal(t, string(chainType), a[j].ChainType())
		assert.Equal(t, a[j].ID(), again[j].ID(), "an instance's bundles are made once")
		assert.NotEqual(t, a[j].PublicKey(), b[j].PublicKey(), "instances have their own keys")
	}
}

func TestEmbeddedClient(t *testing.T) {
	t.Parallel()
	client, err := NewEmbeddedClient(2)
	require.NoError(t, err)
	want, err := EmbeddedKeyBundles(2)
	require.NoError(t, err)

	kbs, err := client.ListKeyBundles(t.Context())
	require.NoError(t, err)
	require.Len(t, kbs, len(want))
	for j, kb := range want {
		assert.Equal(t, kb.ID(), kbs[j].ID)
		assert.Equal(t, kb.PublicKey(), kbs[j].Onchain.PublicKey())
		assert.Equal(t, kb.OffchainPublicKey(), kbs[j].Offchain.OffchainPublicKey())
	}
}
