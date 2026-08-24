package chain

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// nodeKeystore is the keystore a node lends this process: every key it has, for
// every chain it runs.
type nodeKeystore struct {
	accounts []string
	signed   string
}

var _ core.Keystore = (*nodeKeystore)(nil)

func (k *nodeKeystore) Accounts(context.Context) ([]string, error) { return k.accounts, nil }

func (k *nodeKeystore) Sign(_ context.Context, account string, _ []byte) ([]byte, error) {
	k.signed = account
	return []byte("signature"), nil
}

func (k *nodeKeystore) Decrypt(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("chain keys sign; they do not decrypt")
}

const (
	thisChain  = "0x1111111111111111111111111111111111111111"
	otherChain = "0x2222222222222222222222222222222222222222"
)

// TestNarrowed covers what a node's key states do for a relayer: the chain sees
// the account enabled for it, and not the ones enabled for its other chains.
//
// chainlink-evm sends from the enabled address with the highest balance, so an
// unfiltered store is not untidy but wrong - this chain would send as an account
// belonging to another.
func TestNarrowed(t *testing.T) {
	node := &nodeKeystore{accounts: []string{otherChain, thisChain}}
	account := thisChain

	ks, err := narrowed(t.Context(), node, &account)
	require.NoError(t, err)

	accounts, err := ks.Accounts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []string{thisChain}, accounts)

	t.Run("signs as its own account", func(t *testing.T) {
		_, err := ks.Sign(t.Context(), thisChain, []byte("a transaction"))
		require.NoError(t, err)
		assert.Equal(t, thisChain, node.signed)
	})

	t.Run("refuses another chain's", func(t *testing.T) {
		node.signed = ""
		_, err := ks.Sign(t.Context(), otherChain, []byte("a transaction"))
		require.ErrorContains(t, err, "this chain sends from "+thisChain)
		assert.Empty(t, node.signed, "the node's key must not have been reached")
	})
}

// TestNarrowedMustBeHeld is the startup check: a chain configured with an account
// this node has no key for cannot write, and finding that out at the first report
// is finding out too late.
func TestNarrowedMustBeHeld(t *testing.T) {
	node := &nodeKeystore{accounts: []string{otherChain}}
	account := thisChain

	_, err := narrowed(t.Context(), node, &account)
	require.ErrorContains(t, err, "this node holds no key for account "+thisChain)
}

// TestNarrowedUnset is the embedded run: one derived key, nothing to narrow.
func TestNarrowedUnset(t *testing.T) {
	node := &nodeKeystore{accounts: []string{thisChain}}

	for _, account := range []*string{nil, new(string)} {
		ks, err := narrowed(t.Context(), node, account)
		require.NoError(t, err)
		assert.Same(t, core.Keystore(node), ks)
	}
}
