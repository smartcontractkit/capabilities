package ocr

import (
	"context"
	"crypto"
	"crypto/rand"
	"errors"
	"fmt"
	"os"

	"github.com/jmoiron/sqlx"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/models"
)

// keystorePasswordEnvVar is the keystore password used to decrypt the node's
// key ring. This process shares the node's DB (CL_DATABASE_URL) and password.
const keystorePasswordEnvVar = "CL_PASSWORD_KEYSTORE"

// loadPeerKeyring loads the P2P key from the node's keystore so this process
// uses the SAME peer identity as the node it fronts (other DON members expect
// this node's peer ID at this address). It reads the node's existing encrypted
// key ring (the legacy `encrypted_key_rings` table, in chainlink-common's
// corekeys/models format) and decrypts it with the keystore password. This is a
// deliberately small copy of core's keyManager.Unlock using only
// chainlink-common packages, so this binary needn't import chainlink core.
//
// TODO: drop this once the keystore is migrated to chainlink-common's
// keystore.Keystore + pgstore (as chainlink-ccv already uses), after which we
// can LoadKeystore from the shared table directly.
func loadPeerKeyring(ctx context.Context, ds *sqlx.DB) (*peerKeyring, error) {
	var encrypted []byte
	if err := ds.GetContext(ctx, &encrypted, "SELECT encrypted_keys FROM encrypted_key_rings LIMIT 1"); err != nil {
		return nil, fmt.Errorf("failed to read node key ring: %w", err)
	}
	kr, err := models.EncryptedKeyRing{EncryptedKeys: encrypted}.Decrypt(os.Getenv(keystorePasswordEnvVar))
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

// peerKeyring is a ragetypes.PeerKeyring backed by the node's P2P key (a
// crypto.Signer), used in place of the deprecated PeerConfig.PrivKey field.
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
