package ocr

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"
	"strconv"

	"golang.org/x/crypto/curve25519"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// EmbeddedChainType is the chain type of an embedded instance's bundle.
const EmbeddedChainType = "evm"

// embeddedSeedPrefix separates the keys derived here from any other keys derived from an index.
const embeddedSeedPrefix = "cre/capability/embed/ocr/"

// EmbeddedKeyBundle is the key bundle of instance of an embedded run, derived from its index so every instance can
// compute every other's public keys. The keys are public by construction: embed is for local runs and tests.
func EmbeddedKeyBundle(instance int) (KeyBundle, error) {
	onchainSeed := embeddedSeed("onchain", instance)
	offchainSeed := embeddedSeed("offchain", instance)
	kb := &embeddedKeyBundle{
		id:           "embedded-" + strconv.Itoa(instance),
		onchain:      ed25519.NewKeyFromSeed(onchainSeed[:]),
		offchain:     ed25519.NewKeyFromSeed(offchainSeed[:]),
		configSecret: embeddedSeed("config", instance),
	}

	configPublic, err := curve25519.X25519(kb.configSecret[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("failed to derive the config encryption key of instance %d: %w", instance, err)
	}

	copy(kb.configPublic[:], configPublic)
	return kb, nil
}

// NewEmbeddedClient is the Client of instance of an embedded run, which holds only EmbeddedKeyBundle(instance).
func NewEmbeddedClient(instance int) (Client, error) {
	kb, err := EmbeddedKeyBundle(instance)
	if err != nil {
		return nil, err
	}

	return embeddedClient{kb: KeyBundleClient{ID: kb.ID(), ChainType: kb.ChainType(), Onchain: kb, Offchain: kb}}, nil
}

func embeddedSeed(purpose string, instance int) [32]byte {
	return sha256.Sum256([]byte(embeddedSeedPrefix + purpose + "/" + strconv.Itoa(instance)))
}

type embeddedClient struct {
	kb KeyBundleClient
}

func (c embeddedClient) ListKeyBundles(_ context.Context, chainTypes ...string) ([]KeyBundleClient, error) {
	if len(chainTypes) > 0 && !slices.Contains(chainTypes, c.kb.ChainType) {
		return nil, nil
	}

	return []KeyBundleClient{c.kb}, nil
}

func (c embeddedClient) GetKeyBundle(_ context.Context, id string) (KeyBundleClient, error) {
	if id != c.kb.ID {
		return KeyBundleClient{}, fmt.Errorf("%s: %w", id, ErrKeyBundleNotFound)
	}

	return c.kb, nil
}

type embeddedKeyBundle struct {
	id           string
	onchain      ed25519.PrivateKey
	offchain     ed25519.PrivateKey
	configSecret [32]byte
	configPublic ocrtypes.ConfigEncryptionPublicKey
}

var _ KeyBundle = (*embeddedKeyBundle)(nil)

func (b *embeddedKeyBundle) ID() string        { return b.id }
func (b *embeddedKeyBundle) ChainType() string { return EmbeddedChainType }

func (b *embeddedKeyBundle) PublicKey() ocrtypes.OnchainPublicKey {
	return ocrtypes.OnchainPublicKey(b.onchain.Public().(ed25519.PublicKey))
}

func (b *embeddedKeyBundle) Sign(rc ocrtypes.ReportContext, report ocrtypes.Report) ([]byte, error) {
	return ed25519.Sign(b.onchain, embeddedReportHash(rc, report)), nil
}

func (b *embeddedKeyBundle) Verify(publicKey ocrtypes.OnchainPublicKey, rc ocrtypes.ReportContext,
	report ocrtypes.Report, signature []byte) bool {
	return len(publicKey) == ed25519.PublicKeySize &&
		ed25519.Verify(ed25519.PublicKey(publicKey), embeddedReportHash(rc, report), signature)
}

func (b *embeddedKeyBundle) MaxSignatureLength() int { return ed25519.SignatureSize }

func (b *embeddedKeyBundle) OffchainSign(msg []byte) ([]byte, error) {
	return ed25519.Sign(b.offchain, msg), nil
}

func (b *embeddedKeyBundle) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
	var shared [32]byte
	secret, err := curve25519.X25519(b.configSecret[:], point[:])
	if err != nil {
		return shared, err
	}

	copy(shared[:], secret)
	return shared, nil
}

func (b *embeddedKeyBundle) OffchainPublicKey() ocrtypes.OffchainPublicKey {
	return ocrtypes.OffchainPublicKey(b.offchain.Public().(ed25519.PublicKey))
}

func (b *embeddedKeyBundle) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return b.configPublic
}

func embeddedReportHash(rc ocrtypes.ReportContext, report ocrtypes.Report) []byte {
	h := sha256.New()
	h.Write(rc.ConfigDigest[:])
	h.Write(binary.BigEndian.AppendUint32(nil, rc.Epoch))
	h.Write([]byte{rc.Round})
	h.Write(rc.ExtraHash[:])
	h.Write(report)
	return h.Sum(nil)
}
