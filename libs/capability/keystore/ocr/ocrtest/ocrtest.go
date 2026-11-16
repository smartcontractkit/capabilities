// Package ocrtest is an in-memory ocr.Keystore for tests.
package ocrtest

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

// Keystore is the bundles it holds, in order.
type Keystore []*KeyBundle

var _ ocr.Keystore = Keystore(nil)

func (ks Keystore) List(_ context.Context, chainTypes ...string) ([]ocr.KeyBundle, error) {
	var kbs []ocr.KeyBundle
	for _, kb := range ks {
		if len(chainTypes) == 0 || slices.Contains(chainTypes, kb.chainType) {
			kbs = append(kbs, kb)
		}
	}

	return kbs, nil
}

func (ks Keystore) Get(_ context.Context, id string) (ocr.KeyBundle, error) {
	for _, kb := range ks {
		if kb.id == id {
			return kb, nil
		}
	}

	return nil, fmt.Errorf("%s: %w", id, ocr.ErrKeyBundleNotFound)
}

// KeyBundle signs with ed25519 keys derived from its ID, so two bundles with the same ID are the same keys. Its config
// Diffie-Hellman is a hash of its secret and the point, which is enough to tell whether a call reached it.
type KeyBundle struct {
	id, chainType string
	onchain       ed25519.PrivateKey
	offchain      ed25519.PrivateKey
	configSecret  [32]byte
}

var _ ocr.KeyBundle = (*KeyBundle)(nil)

func NewKeyBundle(id, chainType string) *KeyBundle {
	onchainSeed := sha256.Sum256([]byte(id + "/onchain"))
	offchainSeed := sha256.Sum256([]byte(id + "/offchain"))
	return &KeyBundle{
		id:           id,
		chainType:    chainType,
		onchain:      ed25519.NewKeyFromSeed(onchainSeed[:]),
		offchain:     ed25519.NewKeyFromSeed(offchainSeed[:]),
		configSecret: sha256.Sum256([]byte(id + "/config")),
	}
}

func (b *KeyBundle) ID() string        { return b.id }
func (b *KeyBundle) ChainType() string { return b.chainType }

func (b *KeyBundle) PublicKey() ocrtypes.OnchainPublicKey {
	return ocrtypes.OnchainPublicKey(b.onchain.Public().(ed25519.PublicKey))
}

func (b *KeyBundle) Sign(rc ocrtypes.ReportContext, report ocrtypes.Report) ([]byte, error) {
	return ed25519.Sign(b.onchain, sigData(rc, report)), nil
}

func (b *KeyBundle) Verify(publicKey ocrtypes.OnchainPublicKey, rc ocrtypes.ReportContext, report ocrtypes.Report,
	signature []byte) bool {
	return len(publicKey) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(publicKey), sigData(rc, report), signature)
}

func (b *KeyBundle) MaxSignatureLength() int { return ed25519.SignatureSize }

func (b *KeyBundle) OffchainSign(msg []byte) ([]byte, error) {
	return ed25519.Sign(b.offchain, msg), nil
}

func (b *KeyBundle) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
	return sha256.Sum256(append(b.configSecret[:], point[:]...)), nil
}

func (b *KeyBundle) OffchainPublicKey() ocrtypes.OffchainPublicKey {
	return ocrtypes.OffchainPublicKey(b.offchain.Public().(ed25519.PublicKey))
}

func (b *KeyBundle) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return ocrtypes.ConfigEncryptionPublicKey(sha256.Sum256(b.configSecret[:]))
}

func sigData(rc ocrtypes.ReportContext, report ocrtypes.Report) []byte {
	h := sha256.New()
	h.Write(rc.ConfigDigest[:])
	h.Write(binary.BigEndian.AppendUint32(nil, rc.Epoch))
	h.Write([]byte{rc.Round})
	h.Write(rc.ExtraHash[:])
	h.Write(report)
	return h.Sum(nil)
}
