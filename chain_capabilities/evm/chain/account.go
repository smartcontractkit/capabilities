package chain

import (
	"context"
	"fmt"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// narrowed is the keystore this chain signs with: the node's, holding only the
// account this chain sends from.
//
// A node holds a key per chain it runs, and the keystore this process borrows is
// one flat store of all of them - the chain each belongs to lived in the node's
// own evm.key_states table, which is where a relayer's keystore was narrowed
// (see core's relayer_factory, which builds one EthSigner per chain). Reaching
// the chain directly means that narrowing has to happen here instead.
//
// It is not tidiness. chainlink-evm sends from the enabled address with the
// highest balance, so a chain left holding another chain's key can decide to send
// as an account that is not this node's transmitter here.
//
// An empty account leaves the keystore alone: an embedded instance derives one
// key and has nothing to narrow.
func narrowed(ctx context.Context, keystore core.Keystore, account *string) (core.Keystore, error) {
	if account == nil || *account == "" {
		return keystore, nil
	}

	accounts, err := keystore.Accounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to read the accounts this node holds: %w", err)
	}
	// Checked here rather than left to the first transaction: an account this node
	// does not hold is a configuration this chain cannot write with, and saying so at
	// startup is the difference between a chain that does not start and one that reads
	// happily until the first report has to land.
	if !holds(accounts, *account) {
		return nil, fmt.Errorf("this node holds no key for account %s, which is the account this chain sends from", *account)
	}

	return &oneAccount{keystore: keystore, account: *account}, nil
}

func holds(accounts []string, account string) bool {
	for _, held := range accounts {
		if strings.EqualFold(held, account) {
			return true
		}
	}
	return false
}

// oneAccount is the node's keystore, seen through the one account this chain uses.
type oneAccount struct {
	keystore core.Keystore
	account  string
}

var _ core.Keystore = (*oneAccount)(nil)

func (k *oneAccount) Accounts(context.Context) ([]string, error) {
	return []string{k.account}, nil
}

// Sign refuses the node's other accounts rather than passing them through: they
// belong to this node's other chains, and a transaction on this one signed by one
// of them is a nonce this chain does not track.
func (k *oneAccount) Sign(ctx context.Context, account string, data []byte) ([]byte, error) {
	if !strings.EqualFold(account, k.account) {
		return nil, fmt.Errorf("this chain sends from %s, not %s", k.account, account)
	}
	return k.keystore.Sign(ctx, k.account, data)
}

func (k *oneAccount) Decrypt(ctx context.Context, account string, data []byte) ([]byte, error) {
	if !strings.EqualFold(account, k.account) {
		return nil, fmt.Errorf("this chain uses %s, not %s", k.account, account)
	}
	return k.keystore.Decrypt(ctx, k.account, data)
}
