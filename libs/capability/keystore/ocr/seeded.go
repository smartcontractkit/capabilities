package ocr

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// NewSeededKeyBundle is a bundle of ed25519 signing keys and an X25519 config key, all derived from seed. Anyone with
// the seed has the keys, so it is only for embedded runs and tests.
func NewSeededKeyBundle(id, chainType string, seed [32]byte) KeyBundle {
	onchainSeed := sha256.Sum256(append(seed[:], "onchain"...))
	offchainSeed := sha256.Sum256(append(seed[:], "offchain"...))
	configSeed := sha256.Sum256(append(seed[:], "config"...))
	configKey, err := ecdh.X25519().NewPrivateKey(configSeed[:])
	if err != nil {
		// Only a key of the wrong length fails, and a sha256 sum is the right length.
		panic(err)
	}

	return &seededKeyBundle{
		id:        id,
		chainType: chainType,
		onchain:   ed25519.NewKeyFromSeed(onchainSeed[:]),
		offchain:  ed25519.NewKeyFromSeed(offchainSeed[:]),
		configKey: configKey,
	}
}

type seededKeyBundle struct {
	id, chainType string
	onchain       ed25519.PrivateKey
	offchain      ed25519.PrivateKey
	configKey     *ecdh.PrivateKey
}

var _ KeyBundle = (*seededKeyBundle)(nil)

func (b *seededKeyBundle) ID() string        { return b.id }
func (b *seededKeyBundle) ChainType() string { return b.chainType }

func (b *seededKeyBundle) PublicKey() ocrtypes.OnchainPublicKey {
	return ocrtypes.OnchainPublicKey(b.onchain.Public().(ed25519.PublicKey))
}

func (b *seededKeyBundle) Sign(rc ocrtypes.ReportContext, report ocrtypes.Report) ([]byte, error) {
	return ed25519.Sign(b.onchain, reportHash(rc, report)), nil
}

func (b *seededKeyBundle) Verify(publicKey ocrtypes.OnchainPublicKey, rc ocrtypes.ReportContext,
	report ocrtypes.Report, signature []byte) bool {
	return len(publicKey) == ed25519.PublicKeySize &&
		ed25519.Verify(ed25519.PublicKey(publicKey), reportHash(rc, report), signature)
}

func (b *seededKeyBundle) MaxSignatureLength() int { return ed25519.SignatureSize }

func (b *seededKeyBundle) OffchainSign(msg []byte) ([]byte, error) {
	return ed25519.Sign(b.offchain, msg), nil
}

func (b *seededKeyBundle) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
	remote, err := ecdh.X25519().NewPublicKey(point[:])
	if err != nil {
		return [32]byte{}, err
	}

	shared, err := b.configKey.ECDH(remote)
	if err != nil {
		return [32]byte{}, err
	}

	return [32]byte(shared), nil
}

func (b *seededKeyBundle) OffchainPublicKey() ocrtypes.OffchainPublicKey {
	return ocrtypes.OffchainPublicKey(b.offchain.Public().(ed25519.PublicKey))
}

func (b *seededKeyBundle) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return ocrtypes.ConfigEncryptionPublicKey(b.configKey.PublicKey().Bytes())
}

func reportHash(rc ocrtypes.ReportContext, report ocrtypes.Report) []byte {
	h := sha256.New()
	h.Write(rc.ConfigDigest[:])
	h.Write(binary.BigEndian.AppendUint32(nil, rc.Epoch))
	h.Write([]byte{rc.Round})
	h.Write(rc.ExtraHash[:])
	h.Write(report)
	return h.Sum(nil)
}
