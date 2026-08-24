package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// fakeKeystore is a node's keystore as this server sees one: named keys, some of
// them accounts and some of them the protocol's.
type fakeKeystore struct {
	names  []string
	signed string
}

var _ core.Keystore = (*fakeKeystore)(nil)

func (f *fakeKeystore) Accounts(context.Context) ([]string, error) { return f.names, nil }

func (f *fakeKeystore) Decrypt(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("this server serves no decryption")
}

func (f *fakeKeystore) Sign(_ context.Context, account string, _ []byte) ([]byte, error) {
	f.signed = account
	return []byte("signature"), nil
}

const (
	account   = "0x1234567890123456789012345678901234567890"
	peerKey   = "ragep2p_peer/p2p"
	offchain  = "ocr2_offchain/ocr2/ocr2_offchain_encryption"
	onchain   = "ocr2_onchain/ocr2/ocr2_onchain_signing"
	otherAcct = "9WzDXwBbmkg8ZTbNMqUxvQRAyrZzDsGYdLVL9zYtAWWM"
)

// TestAccountsAreAccounts covers what a chain does with this list: it reads every
// name back as an address, so a protocol key among them is not a key it ignores
// but a chain that will not start.
func TestAccountsAreAccounts(t *testing.T) {
	ks := &fakeKeystore{names: []string{peerKey, account, offchain, onchain, otherAcct}}

	reply, err := newKeystoreServer(ks).Accounts(t.Context(), &creproxy.AccountsRequest{})
	require.NoError(t, err)
	assert.Equal(t, []string{account, otherAcct}, reply.GetAccounts())
}

// TestSignRefusesProtocolKeys is the other half, and the one that matters: the
// OCR keys are reached through the signer service, which signs a report under a
// config digest and a sequence number. Signing arbitrary bytes with them here
// would be a way around that.
func TestSignRefusesProtocolKeys(t *testing.T) {
	for _, name := range []string{peerKey, offchain, onchain} {
		t.Run(name, func(t *testing.T) {
			ks := &fakeKeystore{names: []string{name}}

			_, err := newKeystoreServer(ks).Sign(t.Context(), &creproxy.SignRequest{Account: name, Data: []byte("anything")})
			require.Error(t, err)
			assert.Equal(t, codes.PermissionDenied, status.Code(err))
			assert.Empty(t, ks.signed, "the key must not have been reached at all")
		})
	}
}

// TestSignExistenceCheck mirrors what a node's own keystore answers: no data is
// the existence check the interface describes, and the answer is no signature
// rather than a signature of nothing.
func TestSignExistenceCheck(t *testing.T) {
	ks := &fakeKeystore{names: []string{account}}
	server := newKeystoreServer(ks)

	reply, err := server.Sign(t.Context(), &creproxy.SignRequest{Account: account})
	require.NoError(t, err)
	assert.Empty(t, reply.GetSigned())
	assert.Empty(t, ks.signed, "nothing was signed")

	_, err = server.Sign(t.Context(), &creproxy.SignRequest{Account: otherAcct})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestSignWithAnAccount(t *testing.T) {
	ks := &fakeKeystore{names: []string{account}}

	reply, err := newKeystoreServer(ks).Sign(t.Context(), &creproxy.SignRequest{Account: account, Data: []byte("a digest")})
	require.NoError(t, err)
	assert.Equal(t, []byte("signature"), reply.GetSigned())
	assert.Equal(t, account, ks.signed)
}
