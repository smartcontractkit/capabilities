package rage

import (
	"crypto"
	"crypto/ed25519"
	"crypto/rand"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"
)

// NewSignerPeerKeyring adapts a crypto.Signer holding the node's P2P key into the keyring a rage
// peer signs with. Moved here from core's ocrcommon, which a process that only speaks DON-to-DON
// has no other reason to depend on.
func NewSignerPeerKeyring(signer crypto.Signer) (ragetypes.PeerKeyring, error) {
	peerPublicKey, err := ragetypes.PeerPublicKeyFromGenericPublicKey(signer.Public())
	if err != nil {
		return nil, err
	}
	return &signerPeerKeyring{signer: signer, peerPublicKey: peerPublicKey}, nil
}

type signerPeerKeyring struct {
	signer        crypto.Signer
	peerPublicKey ragetypes.PeerPublicKey
}

var _ ragetypes.PeerKeyring = (*signerPeerKeyring)(nil)

func (s *signerPeerKeyring) PublicKey() ragetypes.PeerPublicKey { return s.peerPublicKey }

// Sign produces an EdDSA-Ed25519 signature, as PeerKeyring requires: the key is ed25519, so the
// message is signed whole rather than pre-hashed.
func (s *signerPeerKeyring) Sign(msg []byte) ([]byte, error) {
	return s.signer.Sign(rand.Reader, msg, crypto.Hash(0))
}

// MustNewPeerID returns the peer ID of a freshly generated key, panicking if one cannot be made.
// For tests and local tooling that need a peer ID and do not care which; moved here from core's
// utils package, which has nothing else this needs.
func MustNewPeerID() string {
	pubKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	peerID, err := ragetypes.PeerIDFromPublicKey(pubKey)
	if err != nil {
		panic(err)
	}
	return peerID.String()
}
