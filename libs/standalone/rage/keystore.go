package rage

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/models"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// This file is the node keystore, read: the one thing only a process hosting the node's peer does.
//
// It is here rather than beside the delegating form because of what it costs. The key ring holds a
// key of every kind the node has - cosmos, solana, starknet, TON, stellar - so reading it drags all
// of their libraries in, and a process that merely drives an oracle over someone else's peer has no
// use for any of it. Keeping it in this package means only a binary that hosts a peer pays.

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
func loadPeerKeyring(ctx context.Context, ds *sqlx.DB, password string) (ragetypes.PeerKeyring, error) {
	var encrypted []byte
	if err := ds.GetContext(ctx, &encrypted, "SELECT encrypted_keys FROM encrypted_key_rings LIMIT 1"); err != nil {
		return nil, fmt.Errorf("failed to read node key ring: %w", err)
	}
	kr, err := models.EncryptedKeyRing{EncryptedKeys: encrypted}.Decrypt(password)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt node key ring: %w", err)
	}
	for _, k := range kr.P2P {
		return ocr.NewPeerKeyring(k)
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
