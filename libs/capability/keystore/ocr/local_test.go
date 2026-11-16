package ocr_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr/ocrtest"
)

func TestLocalClient(t *testing.T) {
	t.Parallel()
	evm, sol := ocrtest.NewKeyBundle("evm-1", "evm"), ocrtest.NewKeyBundle("sol-1", "solana")
	client := ocr.NewLocalClient(ocrtest.Keystore{evm, sol})

	all, err := client.ListKeyBundles(t.Context())
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "evm-1", all[0].ID)
	assert.Equal(t, "evm", all[0].ChainType)
	assert.Same(t, evm, all[0].Onchain)
	assert.Same(t, evm, all[0].Offchain)

	solana, err := client.ListKeyBundles(t.Context(), "solana")
	require.NoError(t, err)
	require.Len(t, solana, 1)
	assert.Same(t, sol, solana[0].Onchain)

	kb, err := client.GetKeyBundle(t.Context(), "sol-1")
	require.NoError(t, err)
	assert.Same(t, sol, kb.Offchain)

	_, err = client.GetKeyBundle(t.Context(), "missing")
	require.ErrorIs(t, err, ocr.ErrKeyBundleNotFound)
}
