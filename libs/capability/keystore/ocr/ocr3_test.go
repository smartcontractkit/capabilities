package ocr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

func TestNewMultichainOCR3OnchainKeyring(t *testing.T) {
	t.Parallel()
	evm := newKeyBundle(t, "evm")
	otherEVM := newKeyBundle(t, "evm")
	solana := newKeyBundle(t, "solana")
	client := NewLocalClient(StaticKeystore{evm, otherEVM, solana})
	keyringOf := func(ids ...string) ocr3types.OnchainKeyring[string] {
		bundles := make([]KeyBundleClient, len(ids))
		for i, id := range ids {
			b, err := client.GetKeyBundle(t.Context(), id)
			require.NoError(t, err)
			bundles[i] = b
		}

		k, err := NewMultichainOCR3OnchainKeyring(bundles)
		require.NoError(t, err)
		return k
	}

	signer := keyringOf(evm.ID(), solana.ID())
	verifier := keyringOf(otherEVM.ID())

	report := ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: "evm"}
	sig, err := signer.Sign(ocrtypes.ConfigDigest{1}, 2, report)
	require.NoError(t, err)
	assert.True(t, verifier.Verify(signer.PublicKey(), ocrtypes.ConfigDigest{1}, 2, report, sig),
		"the signer's evm key is found in its multichain key")
	assert.False(t, verifier.Verify(signer.PublicKey(), ocrtypes.ConfigDigest{1}, 3, report, sig))
	assert.False(t, verifier.Verify(verifier.PublicKey(), ocrtypes.ConfigDigest{1}, 2, report, sig))

	stellar := ocr3types.ReportWithInfo[string]{Report: report.Report, Info: "stellar"}
	_, err = signer.Sign(ocrtypes.ConfigDigest{1}, 2, stellar)
	require.ErrorContains(t, err, "no key bundle for chain type stellar")
	assert.False(t, verifier.Verify(signer.PublicKey(), ocrtypes.ConfigDigest{1}, 2, stellar, sig))

	_, err = NewMultichainOCR3OnchainKeyring([]KeyBundleClient{
		{ID: "a", ChainType: "evm", Onchain: evm},
		{ID: "b", ChainType: "evm", Onchain: otherEVM},
	})
	require.ErrorContains(t, err, "both for chain type evm")
}

func TestNewOCR3OnchainKeyring(t *testing.T) {
	t.Parallel()
	signer := NewOCR3OnchainKeyring(newKeyBundle(t, "evm"))
	verifier := NewOCR3OnchainKeyring(newKeyBundle(t, "evm"))
	for _, info := range []string{"evm", "stellar"} {
		report := ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: info}
		sig, err := signer.Sign(ocrtypes.ConfigDigest{1}, 2, report)
		require.NoError(t, err)
		assert.True(t, verifier.Verify(signer.PublicKey(), ocrtypes.ConfigDigest{1}, 2, report, sig))
		assert.False(t, verifier.Verify(signer.PublicKey(), ocrtypes.ConfigDigest{1}, 3, report, sig))
	}
}

func newKeyBundle(t *testing.T, chainType string) KeyBundle {
	t.Helper()
	kb, err := NewKeyBundle(chainType)
	require.NoError(t, err)
	return kb
}
