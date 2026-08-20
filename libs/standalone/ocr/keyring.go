package ocr

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strconv"
	"sync"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

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
	return NewPeerKeyring(ed25519.NewKeyFromSeed(seed[:]))
}

// NewPeerKeyring returns signer as a rage peer keyring, deriving the public half it announces
// under.
//
// Exported because a P2P key reaches this framework two ways - derived from an instance index here,
// or unlocked from a node keystore (see libs/standalone/rage) - and both end up needing the same
// wrapper. Used in place of the deprecated PeerConfig.PrivKey field.
func NewPeerKeyring(signer crypto.Signer) (ragetypes.PeerKeyring, error) {
	pub, err := ragetypes.PeerPublicKeyFromGenericPublicKey(signer.Public())
	if err != nil {
		return nil, fmt.Errorf("failed to derive the peer public key: %w", err)
	}
	return &peerKeyring{signer: signer, publicKey: pub}, nil
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

// embedBundles is the OCR key bundle of each instance in this process, kept in one place so that
// every instance sees every other instance's.
//
// It is package-level for the reason embedNetwork is (see inproc.go): there is exactly one process,
// so there is exactly one set of instances inside it, and an OCR configuration naming them all has
// to be built from the same keys the instances sign with. That is also why these are generated
// rather than derived from the index the way the P2P identity is - a key derived from an index would
// be reproducible outside the process too, which nothing here needs, and secp256k1 key generation
// ignores the material it is offered anyway (crypto/ecdsa generates from the system CSPRNG, so a
// seeded reader buys nothing).
//
// EVM, because a capability DON's members are registered with an EVM signing key: an embedded run
// should exercise the signing and verification path a real one takes.
var embedBundles = &bundleSet{bundles: map[int]ocr2key.KeyBundle{}}

// bundleSet hands out one OCR key bundle per instance index, making it the first time it is asked
// for. Which instance asks first does not matter: an instance asks for its own to sign with, and the
// configuration asks for all of them to name the DON, and either order ends up with the same set.
type bundleSet struct {
	mu      sync.Mutex
	bundles map[int]ocr2key.KeyBundle
}

func (s *bundleSet) get(i int) (ocr2key.KeyBundle, error) {
	if i < 0 {
		return nil, fmt.Errorf("cannot resolve the OCR key bundle of instance %d: an instance index is not negative", i)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if bundle, ok := s.bundles[i]; ok {
		return bundle, nil
	}
	bundle, err := ocr2key.New(corekeys.EVM)
	if err != nil {
		return nil, fmt.Errorf("failed to create an OCR key bundle for instance %d: %w", i, err)
	}
	s.bundles[i] = bundle
	return bundle, nil
}

// EmbeddedOCR2Bundle returns the OCR2 key bundle instance i signs with: its protocol messages with
// the offchain half, and its reports with the onchain one.
//
// An embedded instance has no node keystore to take one from, so the process keeps one for each of
// them - see embedBundles, and EmbeddedOCRConfig for the configuration that lists their public
// halves as the DON. Exported for the hosting form, which serves this bundle where a real one would
// serve the node's (see libs/standalone/rage).
func EmbeddedOCR2Bundle(i int) (ocr2key.KeyBundle, error) { return embedBundles.get(i) }

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
