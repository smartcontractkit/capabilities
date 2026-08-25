package auth

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// DONs is who a gateway will talk to: the nodes of each DON it serves, by
// address.
//
// It is the same fact the connection manager reads out of its configuration
// today - a DON is a list of node addresses - and it is what a signature is
// checked against, so a node that is not listed cannot connect however good its
// signature is.
type DONs map[string][]string

var _ Verifier = (DONs)(nil)

// Nodes returns the members of donID.
func (d DONs) Nodes(donID string) ([]string, bool) {
	nodes, ok := d[donID]
	if !ok {
		return nil, false
	}
	return normalised(nodes), true
}

// Verify reports whether sig over hash was made by the key behind address.
func (d DONs) Verify(address string, hash, sig []byte) bool {
	signer, err := Recover(hash, sig)
	if err != nil {
		return false
	}
	return strings.EqualFold(signer, address)
}

// DonIDs returns the DONs served, sorted, for a gateway that has to report what
// it knows.
func (d DONs) DonIDs() []string { return slices.Sorted(maps.Keys(d)) }

// KeystoreSigner returns a Signer over a keystore account: what signs is the key behind
// address, wherever that keystore keeps it - in this process for a local run, or
// in crecore for a node.
//
// The keystore is handed a digest and returns a signature, which is the whole of
// what this needs: the key does not move, and this package never sees one.
func KeystoreSigner(keystore core.Keystore, address string) SignerFunc {
	return func(ctx context.Context, hash []byte) ([]byte, error) {
		signature, err := keystore.Sign(ctx, address, hash)
		if err != nil {
			return nil, fmt.Errorf("failed to sign as %s: %w", address, err)
		}
		if len(signature) != SignatureLen {
			return nil, fmt.Errorf("the keystore signed as %s with %d bytes, want %d", address, len(signature), SignatureLen)
		}
		return signature, nil
	}
}

// SignerFunc is a function that signs, as a Signer.
type SignerFunc func(ctx context.Context, hash []byte) ([]byte, error)

func (f SignerFunc) Sign(ctx context.Context, hash []byte) ([]byte, error) { return f(ctx, hash) }

// AddressOf is the account a secp256k1 public key signs as: the last 20 bytes of
// the keccak256 of its uncompressed form, minus the leading tag byte.
//
// Computed here rather than taken from a chain library because this package is
// about identity rather than about a chain, and because the store hands back
// exactly the uncompressed form this needs.
func AddressOf(publicKey []byte) (string, error) {
	if len(publicKey) != 65 || publicKey[0] != 4 {
		return "", errors.New("expected an uncompressed secp256k1 public key")
	}

	hash := sha3.NewLegacyKeccak256()
	hash.Write(publicKey[1:])
	return "0x" + hex.EncodeToString(hash.Sum(nil)[12:]), nil
}

// normalised lowercases addresses so that two spellings of one account are one
// account. Nothing here is case-sensitive but hex is written both ways.
func normalised(addresses []string) []string {
	lowered := make([]string, 0, len(addresses))
	for _, address := range addresses {
		lowered = append(lowered, strings.ToLower(address))
	}
	return lowered
}

// Recover says which account signed hash.
//
// The signature is the 65-byte form everything in this system uses: r, s, and a
// recovery byte of 0 or 1. dcrd wants that byte first and offset by 27, which is
// the only difference between the two spellings of the same thing.
//
// It is here rather than borrowed from a chain library because this package is
// about identity, and because a repository that is not about one chain should not
// take a chain's client to check a signature.
func Recover(hash, signature []byte) (string, error) {
	if len(signature) != SignatureLen {
		return "", fmt.Errorf("a signature is %d bytes, got %d", SignatureLen, len(signature))
	}
	recovery := signature[SignatureLen-1]
	if recovery > 1 {
		// Some callers write 27/28 rather than 0/1; both mean the same two values.
		if recovery != 27 && recovery != 28 {
			return "", fmt.Errorf("%d is not a recovery byte", recovery)
		}
		recovery -= 27
	}

	compact := make([]byte, 0, SignatureLen)
	compact = append(compact, recovery+27)
	compact = append(compact, signature[:SignatureLen-1]...)

	public, _, err := ecdsa.RecoverCompact(compact, hash)
	if err != nil {
		return "", fmt.Errorf("failed to recover the signer: %w", err)
	}
	return AddressOf(public.SerializeUncompressed())
}

func decodeAddress(address string) ([]byte, error) {
	trimmed := strings.TrimPrefix(strings.ToLower(address), "0x")
	if len(trimmed) != 40 {
		return nil, fmt.Errorf("%q is not an address", address)
	}
	return hex.DecodeString(trimmed)
}
