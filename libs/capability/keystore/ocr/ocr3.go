package ocr

import (
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

// NewOCR3OnchainKeyring is keyring as an OCR3 keyring, which signs every report with it, whatever chain type the
// report's info names.
func NewOCR3OnchainKeyring(keyring ocrtypes.OnchainKeyring) ocr3types.OnchainKeyring[string] {
	return ocr3Keyring{keyring}
}

type ocr3Keyring struct {
	ocrtypes.OnchainKeyring
}

func (k ocr3Keyring) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[string]) ([]byte, error) {
	return k.OnchainKeyring.Sign(reportContext(digest, seqNr), report.Report)
}

func (k ocr3Keyring) Verify(publicKey ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[string], signature []byte) bool {
	return k.OnchainKeyring.Verify(publicKey, reportContext(digest, seqNr), report.Report, signature)
}

// NewMultichainOCR3OnchainKeyring is an OCR3 keyring that signs a report with the bundle of the chain type its info
// names, as the node's own OCR3 oracles do. Its public key lists every bundle's, which is how the registry's config
// lists the node as a signer. At most one bundle may be of each chain type.
func NewMultichainOCR3OnchainKeyring(bundles []KeyBundleClient) (ocr3types.OnchainKeyring[string], error) {
	k := &multichainKeyring{bundles: map[string]ocrtypes.OnchainKeyring{}}
	keys := ocr2key.MultichainPublicKey{}
	for _, b := range bundles {
		if _, ok := k.bundles[b.ChainType]; ok {
			return nil, fmt.Errorf("key bundles %s and another are both for chain type %s", b.ID, b.ChainType)
		}

		k.bundles[b.ChainType] = b.Onchain
		keys[b.ChainType] = b.Onchain.PublicKey()
		k.maxSignatureLength = max(k.maxSignatureLength, b.Onchain.MaxSignatureLength())
	}

	publicKey, err := keys.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("failed to encode the onchain public keys: %w", err)
	}

	k.publicKey = publicKey
	return k, nil
}

type multichainKeyring struct {
	bundles            map[string]ocrtypes.OnchainKeyring
	publicKey          ocrtypes.OnchainPublicKey
	maxSignatureLength int
}

func (k *multichainKeyring) PublicKey() ocrtypes.OnchainPublicKey { return k.publicKey }

func (k *multichainKeyring) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[string]) ([]byte, error) {
	bundle, ok := k.bundles[report.Info]
	if !ok {
		return nil, fmt.Errorf("the node has no key bundle for chain type %s", report.Info)
	}

	return bundle.Sign(reportContext(digest, seqNr), report.Report)
}

func (k *multichainKeyring) Verify(publicKey ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[string], signature []byte) bool {
	bundle, ok := k.bundles[report.Info]
	if !ok {
		return false
	}

	var keys ocr2key.MultichainPublicKey
	if err := keys.UnmarshalBinary(publicKey); err != nil {
		return false
	}

	key, ok := keys[report.Info]
	return ok && bundle.Verify(key, reportContext(digest, seqNr), report.Report, signature)
}

func (k *multichainKeyring) MaxSignatureLength() int { return k.maxSignatureLength }

// reportContext is how the node's OCR3 oracles sign with OCR2 key bundles: the sequence number in place of the epoch.
func reportContext(digest ocrtypes.ConfigDigest, seqNr uint64) ocrtypes.ReportContext {
	return ocrtypes.ReportContext{
		ReportTimestamp: ocrtypes.ReportTimestamp{
			ConfigDigest: digest,
			Epoch:        uint32(seqNr), //nolint:gosec // seqNr maps to the uint32 epoch field
		},
	}
}
