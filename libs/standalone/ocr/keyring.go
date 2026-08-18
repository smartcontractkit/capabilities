package ocr

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/models"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

// loadPeerKeyring loads the P2P key from the node's keystore so this process
// uses the SAME peer identity as the node it fronts (other DON members expect
// this node's peer ID at this address). It reads the node's existing encrypted
// key ring (the legacy `encrypted_key_rings` table, in chainlink-common's
// corekeys/models format) and decrypts it with the keystore password. This is a
// deliberately small copy of core's keyManager.Unlock using only
// chainlink-common packages, so this binary needn't import chainlink core.
//
// password is the node's keystore password: this process shares the node's
// database and therefore its keystore password.
//
// TODO: drop this once the keystore is migrated to chainlink-common's
// keystore.Keystore + pgstore (as chainlink-ccv already uses), after which we
// can LoadKeystore from the shared table directly.
func loadPeerKeyring(ctx context.Context, ds *sqlx.DB, password string) (*peerKeyring, error) {
	var encrypted []byte
	if err := ds.GetContext(ctx, &encrypted, "SELECT encrypted_keys FROM encrypted_key_rings LIMIT 1"); err != nil {
		return nil, fmt.Errorf("failed to read node key ring: %w", err)
	}
	kr, err := models.EncryptedKeyRing{EncryptedKeys: encrypted}.Decrypt(password)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt node key ring: %w", err)
	}
	for _, k := range kr.P2P {
		pub, perr := ragetypes.PeerPublicKeyFromGenericPublicKey(k.Public())
		if perr != nil {
			return nil, fmt.Errorf("failed to derive peer public key: %w", perr)
		}
		return &peerKeyring{signer: k, publicKey: pub}, nil
	}
	return nil, errors.New("no P2P key found in node key ring")
}

// loadOCR2Bundle loads the node's OCR2 key bundle from the same key ring the P2P
// key came from, so the oracle signing done on a capability's behalf is done
// with the node's own OCR identity - the one the registry lists as a signer.
//
// bundleID names which bundle when the node has several. A node running one
// capability DON usually has one, and naming it then would be one more thing to
// keep in step with the keystore, so an empty bundleID takes the only one there
// is and refuses to guess between several.
func loadOCR2Bundle(ctx context.Context, ds *sqlx.DB, password, bundleID string) (ocr2key.KeyBundle, error) {
	var encrypted []byte
	if err := ds.GetContext(ctx, &encrypted, "SELECT encrypted_keys FROM encrypted_key_rings LIMIT 1"); err != nil {
		return nil, fmt.Errorf("failed to read node key ring: %w", err)
	}
	kr, err := models.EncryptedKeyRing{EncryptedKeys: encrypted}.Decrypt(password)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt node key ring: %w", err)
	}

	if bundleID != "" {
		bundle, ok := kr.OCR2[bundleID]
		if !ok {
			return nil, fmt.Errorf("no OCR2 key bundle %q in the node key ring", bundleID)
		}
		return bundle, nil
	}

	switch len(kr.OCR2) {
	case 0:
		return nil, errors.New("no OCR2 key bundle found in the node key ring")
	case 1:
		for _, bundle := range kr.OCR2 {
			return bundle, nil
		}
	}

	ids := make([]string, 0, len(kr.OCR2))
	for id := range kr.OCR2 {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return nil, fmt.Errorf("the node key ring holds %d OCR2 key bundles (%s), so --ocr.key-bundle-id must say which one to sign with",
		len(ids), strings.Join(ids, ", "))
}

// deterministicSeedPrefix domain-separates the derived instance keys, so the seeds are specific
// to this framework's embed mode and cannot collide with another scheme deriving keys from an
// index. Changing it changes every derived peer ID.
const deterministicSeedPrefix = "cre/standalone/instance/"

// DeterministicKeyring returns the P2P identity of instance i of a multi-instance run, derived
// from i rather than read from a keystore.
//
// Embedded instances have no keystore to borrow an identity from - they exist to run a DON on one
// machine without the database and operator setup a real node needs - so their keys come from the
// instance index. Deriving them rather than generating them is what makes that usable: the peer
// IDs of a run are known before it starts, so the OCR configs, registry entries and expectations
// that have to name the DON's members can be computed from the instance count alone (see
// DeterministicPeerID), and they are the same on every run and every machine.
//
// These keys are public by construction. Nothing derived this way is a secret, and nothing that
// matters may be protected by one: embed mode is for local runs and tests.
func DeterministicKeyring(i int) (ragetypes.PeerKeyring, error) {
	seed := sha256.Sum256([]byte(deterministicSeedPrefix + strconv.Itoa(i)))
	key := ed25519.NewKeyFromSeed(seed[:])
	pub, err := ragetypes.PeerPublicKeyFromGenericPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("failed to derive peer public key for instance %d: %w", i, err)
	}
	return &peerKeyring{signer: key, publicKey: pub}, nil
}

// DeterministicPeerID returns the peer ID DeterministicKeyring gives instance i, so a caller
// configuring a DON of embedded instances can name its members without starting them.
func DeterministicPeerID(i int) (ragetypes.PeerID, error) {
	keyring, err := DeterministicKeyring(i)
	if err != nil {
		return ragetypes.PeerID{}, err
	}
	return ragetypes.PeerIDFromKeyring(keyring), nil
}

// peerKeyring is a ragetypes.PeerKeyring backed by a P2P key (a crypto.Signer): the node's own,
// loaded from its keystore, or one derived from an instance index. Used in place of the deprecated
// PeerConfig.PrivKey field.
type peerKeyring struct {
	signer    crypto.Signer
	publicKey ragetypes.PeerPublicKey
}

var _ ragetypes.PeerKeyring = (*peerKeyring)(nil)

// Sign returns an EdDSA-Ed25519 signature over msg, as required by PeerKeyring.
func (k *peerKeyring) Sign(msg []byte) ([]byte, error) {
	return k.signer.Sign(rand.Reader, msg, crypto.Hash(0))
}

func (k *peerKeyring) PublicKey() ragetypes.PeerPublicKey {
	return k.publicKey
}
