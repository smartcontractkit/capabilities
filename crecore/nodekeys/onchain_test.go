package nodekeys

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

// TestOnchainKeyring_InteropWithKeyBundle is the test that matters: a signature
// this keyring makes with a key in the new keystore has to be one an oracle
// holding a legacy key bundle accepts, and the other way round. The two are
// members of the same DON, so what they disagree about is not a bug that shows up
// as a bug - it shows up as a DON that will not agree.
func TestOnchainKeyring_InteropWithKeyBundle(t *testing.T) {
	const family = "evm"

	ctx := t.Context()
	digest := ocrtypes.ConfigDigest{1, 2, 3}
	const seqNr = uint64(42)
	report := ocr3types.ReportWithInfo[[]byte]{Report: []byte("a report")}

	// This node: a key in the new keystore, signed for through it.
	ks, err := keystore.LoadKeystore(ctx, keystore.NewMemoryStorage(), "password")
	require.NoError(t, err)
	_, err = ks.CreateKeys(ctx, keystore.CreateKeysRequest{Keys: []keystore.CreateKeyRequest{{
		KeyName: onchainKeyName("ocr2"),
		KeyType: keystore.ECDSA_S256,
	}}})
	require.NoError(t, err)

	mine, err := newOnchainKeyring(ctx, ks, "ocr2", family)
	require.NoError(t, err)

	// Another member: a legacy key bundle, which is what a node still holds.
	bundle, err := ocr2key.New(corekeys.EVM)
	require.NoError(t, err)
	theirKey, err := ocr2key.MarshalMultichainPublicKey(map[string]ocrtypes.OnchainPublicKey{family: bundle.PublicKey()})
	require.NoError(t, err)

	t.Run("a key bundle accepts what this keyring signed", func(t *testing.T) {
		signature, err := mine.Sign(digest, seqNr, report)
		require.NoError(t, err)
		require.Len(t, signature, mine.MaxSignatureLength())

		// What the bundle-holding member does with the signature and this node's
		// advertised public key.
		keys, err := ocr2key.UnmarshalMultichainPublicKey(mine.PublicKey())
		require.NoError(t, err, "the public key this keyring announces must be the encoding a configuration lists")
		require.Contains(t, keys, family)

		blob := ocr2key.ReportToSigData(ocr2key.OCR3ReportContext(digest, seqNr), report.Report)
		assert.True(t, ocr2key.EvmVerifyBlob(keys[family], blob, signature))
	})

	t.Run("this keyring accepts what a key bundle signed", func(t *testing.T) {
		signature, err := bundle.Sign(ocr2key.OCR3ReportContext(digest, seqNr), report.Report)
		require.NoError(t, err)

		assert.True(t, mine.Verify(theirKey, digest, seqNr, report, signature))
	})

	t.Run("a signature over another round is refused", func(t *testing.T) {
		signature, err := mine.Sign(digest, seqNr, report)
		require.NoError(t, err)

		assert.False(t, mine.Verify(mine.PublicKey(), digest, seqNr+1, report, signature))
		assert.False(t, mine.Verify(mine.PublicKey(), ocrtypes.ConfigDigest{9}, seqNr, report, signature))
		assert.False(t, mine.Verify(theirKey, digest, seqNr, report, signature),
			"another member's key must not verify this node's signature")
	})

	// Pinned: whatever puts the key in the store has to use this exact name, and it is
	// in another repository (chainlink's keyseed package).
	t.Run("the onchain key's name is the agreed one", func(t *testing.T) {
		assert.Equal(t, "ocr2_onchain/ocr2/ocr2_onchain_signing", onchainKeyName("ocr2"))
	})

	t.Run("a keyring with no family announces nothing", func(t *testing.T) {
		_, err := newOnchainKeyring(ctx, ks, "ocr2", "")
		assert.ErrorContains(t, err, "must say which signing family")
	})
}
