package nodekeys

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/crypto/sha3"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

// OnchainKeyring signs the reports an OCR3 round produces, with a key it never
// holds: the signing is a call into the keystore, which is what lets the key stay
// in the process that unlocked it.
//
// The rules it follows are not its own. What a round's bytes are (ocr2key's
// ReportToSigData over OCR3ReportContext) and how a member's public key is
// written in a configuration (the multichain encoding) are properties of the
// protocol and the registry, shared with every other implementation of them: two
// oracles that disagree about either produce signatures the other rejects, and
// that shows up as a DON which will not come to consensus rather than as anything
// anyone can see.
type OnchainKeyring struct {
	keystore keystore.Keystore
	keyName  string

	// address is this key's EVM address, which is what libocr calls the onchain
	// public key for this family.
	address []byte
	// publicKey is that address in the multichain encoding a configuration lists
	// members by, computed once here so a family that cannot be encoded fails while
	// the keyring is being built.
	publicKey ocrtypes.OnchainPublicKey
}

var _ ocr3types.OnchainKeyring[[]byte] = (*OnchainKeyring)(nil)

// The onchain key's path, alongside the offchain keys chainlink-common's
// ocr2offchain puts under the same keyring name.
//
// Spelled here and in whatever writes the key - for a node, chainlink's keyseed
// package - because the two are different repositories. A disagreement is a key
// this cannot find, which is a startup error rather than a bad signature.
const (
	// PrefixOCR2Onchain namespaces the onchain key, the way chainlink-common's own
	// packages namespace theirs. Exported because it is also what says this key is
	// the protocol's rather than a chain account: see crecore's keystore server.
	PrefixOCR2Onchain = "ocr2_onchain"

	onchainSigning = "ocr2_onchain_signing"
)

// onchainKeyName is where the onchain key sits relative to the OCR keyring's name.
func onchainKeyName(keyring string) string {
	return keystore.NewKeyPath(PrefixOCR2Onchain, keyring, onchainSigning).String()
}

func newOnchainKeyring(ctx context.Context, ks keystore.Keystore, keyring, family string) (*OnchainKeyring, error) {
	if family == "" {
		return nil, errors.New("an onchain keyring must say which signing family it is for")
	}

	name := onchainKeyName(keyring)
	keys, err := ks.GetKeys(ctx, keystore.GetKeysRequest{KeyNames: []string{name}})
	if err != nil {
		return nil, fmt.Errorf("failed to read the OCR onchain key %q: %w", name, err)
	}
	if len(keys.Keys) != 1 {
		return nil, fmt.Errorf("expected one OCR onchain key named %q, found %d", name, len(keys.Keys))
	}

	address, err := evmAddress(keys.Keys[0].KeyInfo.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("the OCR onchain key %q is not a secp256k1 key: %w", name, err)
	}

	publicKey, err := ocr2key.MarshalMultichainPublicKey(map[string]ocrtypes.OnchainPublicKey{family: address})
	if err != nil {
		return nil, fmt.Errorf("failed to encode the %s onchain public key: %w", family, err)
	}
	if len(publicKey) == 0 {
		return nil, fmt.Errorf("%q is not a known signing family, so a keyring for it would announce nothing", family)
	}

	return &OnchainKeyring{keystore: ks, keyName: name, address: address, publicKey: publicKey}, nil
}

// PublicKey is what a configuration lists this oracle under.
func (k *OnchainKeyring) PublicKey() ocrtypes.OnchainPublicKey { return k.publicKey }

// MaxSignatureLength is what libocr budgets for a signature from this keyring: a
// secp256k1 signature with its recovery byte.
func (k *OnchainKeyring) MaxSignatureLength() int { return 65 }

// Sign signs a round's report. The digest it signs is the protocol's, not this
// keyring's: see ocr2key.ReportToSigData.
func (k *OnchainKeyring) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[[]byte]) ([]byte, error) {
	signed, err := k.keystore.Sign(context.Background(), keystore.SignRequest{
		KeyName: k.keyName,
		Data:    ocr2key.ReportToSigData(ocr2key.OCR3ReportContext(digest, seqNr), report.Report),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to sign the report: %w", err)
	}
	return signed.Signature, nil
}

// Verify checks a peer's signature. It needs no key of this node's, so it is done
// here rather than asked of anything: every message received would otherwise cost
// a round trip.
func (k *OnchainKeyring) Verify(publicKey ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[[]byte], signature []byte) bool {
	keys, err := ocr2key.UnmarshalMultichainPublicKey(publicKey)
	if err != nil {
		// Not multichain: a member listed by its bare key for this family.
		keys = map[string]ocrtypes.OnchainPublicKey{}
	}

	blob := ocr2key.ReportToSigData(ocr2key.OCR3ReportContext(digest, seqNr), report.Report)
	if len(keys) == 0 {
		return ocr2key.EvmVerifyBlob(publicKey, blob, signature)
	}
	for _, key := range keys {
		if ocr2key.EvmVerifyBlob(key, blob, signature) {
			return true
		}
	}
	return false
}

// evmAddress is the address of a secp256k1 public key: the last 20 bytes of the
// keccak256 of its uncompressed form, minus the leading tag byte.
//
// It is computed here rather than taken from a chain's library because this
// package is deliberately chain-agnostic, and because the store hands back exactly
// the uncompressed form this needs.
func evmAddress(publicKey []byte) ([]byte, error) {
	const uncompressedLength = 65
	if len(publicKey) != uncompressedLength {
		return nil, fmt.Errorf("public key is %d bytes, want %d", len(publicKey), uncompressedLength)
	}

	hash := sha3.NewLegacyKeccak256()
	if _, err := hash.Write(publicKey[1:]); err != nil {
		return nil, err
	}
	return hash.Sum(nil)[12:], nil
}
