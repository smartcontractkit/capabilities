package chain

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// deterministicSeedPrefix domain-separates these keys from anything else derived
// from an instance index. Changing it changes every derived address.
const deterministicSeedPrefix = "cre/standalone/instance/chain/evm/"

// DeterministicKeystore is the keystore instance i of a multi-instance local run
// signs with: one key, derived from the index rather than borrowed from a node.
//
// A process running beside a node signs with the node's keys, and an embedded run
// has no node - that is what embedding means. Deriving rather than generating is
// what makes it usable: the addresses are known before anything starts, so
// whatever has to fund them, or list them as transmitters, can be set up from the
// instance count alone (see DeterministicAddress).
//
// These keys are public by construction. Nothing derived this way is a secret,
// and nothing that matters may be protected by one: embed is for local runs and
// tests.
func DeterministicKeystore(instance int) (core.Keystore, error) {
	key, err := DeterministicKey(instance)
	if err != nil {
		return nil, err
	}
	return &localKeystore{address: crypto.PubkeyToAddress(key.PublicKey), key: key}, nil
}

// KeystoreFromPrivateKey is the keystore an embedded run signs with when it is
// given a key rather than left to derive one.
//
// An embedded run has no node to borrow keys from, so it derives them - which is
// what makes a local run reproducible, and what makes its accounts unfunded
// everywhere that matters. A run pointed at a real chain needs an account that
// exists on it, and this is where that account comes from.
//
// The key is a secret this process then holds, which is the thing every other
// part of this design avoids. That is the trade embedding already makes: it is
// for local runs and tests, and a node beside a real deployment signs through
// crecore and never sees a key at all.
func KeystoreFromPrivateKey(hexKey string) (core.Keystore, error) {
	key, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimSpace(hexKey), "0x"))
	if err != nil {
		return nil, fmt.Errorf("failed to read the configured private key: %w", err)
	}
	return &localKeystore{address: crypto.PubkeyToAddress(key.PublicKey), key: key}, nil
}

// DeterministicKey returns the chain key of instance i.
func DeterministicKey(instance int) (*ecdsa.PrivateKey, error) {
	seed := sha256.Sum256([]byte(deterministicSeedPrefix + strconv.Itoa(instance)))
	key, err := crypto.ToECDSA(seed[:])
	if err != nil {
		return nil, fmt.Errorf("failed to derive the chain key of instance %d: %w", instance, err)
	}
	return key, nil
}

// DeterministicAddress returns the account DeterministicKey gives instance i, so
// a caller preparing a run - funding the accounts, naming them as transmitters -
// can do it without starting anything.
func DeterministicAddress(instance int) (common.Address, error) {
	key, err := DeterministicKey(instance)
	if err != nil {
		return common.Address{}, err
	}
	return crypto.PubkeyToAddress(key.PublicKey), nil
}

// localKeystore is the one derived key, as core.Keystore.
type localKeystore struct {
	address common.Address
	key     *ecdsa.PrivateKey
}

var _ core.Keystore = (*localKeystore)(nil)

func (k *localKeystore) Accounts(context.Context) ([]string, error) {
	return []string{k.address.Hex()}, nil
}

// Sign signs the digest it is given, and only for the account it holds: an
// instance asked to sign as another instance is a run that has confused its
// members for each other.
func (k *localKeystore) Sign(_ context.Context, account string, data []byte) ([]byte, error) {
	if !strings.EqualFold(account, k.address.Hex()) {
		return nil, fmt.Errorf("this instance signs as %s, not %s", k.address, account)
	}
	if len(data) == 0 {
		// The existence check core.Keystore describes, which answers with no signature
		// rather than a signature of nothing.
		return nil, nil
	}
	return crypto.Sign(data, k.key)
}

func (k *localKeystore) Decrypt(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("chain keys sign; they do not decrypt")
}
