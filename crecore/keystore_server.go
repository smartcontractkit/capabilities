package main

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	"github.com/smartcontractkit/chainlink-common/keystore/ocr2offchain"
	"github.com/smartcontractkit/chainlink-common/keystore/ragep2p"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/crecore/nodekeys"
)

// keystoreServer signs with the node's chain keys for capabilities that hold
// none.
//
// It is the signer server's trade for the other half of what a node's identity
// can do: a chain capability transmits as this node, which means signing as the
// account the on-chain registry lists as this node's transmitter. That account's
// key is in the keystore this process unlocked, and stays there - what crosses
// the wire is a digest going out and a signature coming back.
type keystoreServer struct {
	creproxy.UnimplementedKeystoreServer

	keystore core.Keystore
}

var _ creproxy.KeystoreServer = (*keystoreServer)(nil)

func newKeystoreServer(keystore core.Keystore) *keystoreServer {
	return &keystoreServer{keystore: keystore}
}

// protocolNamespaces are the keys this process runs protocols with rather than
// holds accounts under: the peer identity and the two halves of the OCR keyring.
//
// The keystore is one store of named keys, so a node's chain accounts sit in it
// beside these. They are not accounts and must not be offered as ones: a caller
// listing accounts is asking what it may transmit as, and a chain reading
// "ocr2_offchain/ocr2/ocr2_offchain_encryption" back as an address fails to start.
// Signing with them is worse - the OCR keys are reached through the signer
// service, which binds a signature to a report and a config digest, and signing a
// digest with them here would be a way around that.
var protocolNamespaces = []string{
	ragep2p.PrefixPeerKeyring,
	ocr2offchain.PrefixOCR2Offchain,
	nodekeys.PrefixOCR2Onchain,
}

// isProtocolKey reports whether name is one of those, by its leading path
// segment: the keystore names keys as "/"-joined paths, and these three own the
// namespaces they are the root of.
func isProtocolKey(name string) bool {
	for _, namespace := range protocolNamespaces {
		if name == namespace || strings.HasPrefix(name, namespace+"/") {
			return true
		}
	}
	return false
}

// Accounts is the accounts this node can transmit as: every key in the keystore
// that is not one of the protocol's own.
func (s *keystoreServer) Accounts(ctx context.Context, _ *creproxy.AccountsRequest) (*creproxy.AccountsReply, error) {
	all, err := s.keystore.Accounts(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	accounts := make([]string, 0, len(all))
	for _, account := range all {
		if isProtocolKey(account) {
			continue
		}
		accounts = append(accounts, account)
	}
	return &creproxy.AccountsReply{Accounts: accounts}, nil
}

// Sign signs the digest it is given with the named account's key.
//
// An unknown account is the caller's mistake rather than this process's failure -
// it is transmitting from an account this node does not hold - so it comes back
// as InvalidArgument, which is the difference between "fix your configuration"
// and "retry".
func (s *keystoreServer) Sign(ctx context.Context, req *creproxy.SignRequest) (*creproxy.SignReply, error) {
	if req.GetAccount() == "" {
		return nil, status.Error(codes.InvalidArgument, "no account to sign with")
	}
	// A protocol key is not an account, and this is not the way to sign with one: the
	// signer service signs reports with the OCR keys, over a config digest and a
	// sequence number, and this would sign whatever bytes it was handed.
	if isProtocolKey(req.GetAccount()) {
		return nil, status.Errorf(codes.PermissionDenied, "%s is one of this node's protocol keys, not an account it transmits from", req.GetAccount())
	}

	// No data is the existence check the core.Keystore interface describes, which a
	// node's own keystore answers with no signature rather than a signature of
	// nothing. Answered here for the same reason: what asks is chainlink-evm, which
	// asks a node's keystore the same question.
	if len(req.GetData()) == 0 {
		accounts, err := s.keystore.Accounts(ctx)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		for _, account := range accounts {
			if strings.EqualFold(account, req.GetAccount()) {
				return &creproxy.SignReply{}, nil
			}
		}
		return nil, status.Errorf(codes.InvalidArgument, "this node holds no key for account %s", req.GetAccount())
	}

	signed, err := s.keystore.Sign(ctx, req.GetAccount(), req.GetData())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &creproxy.SignReply{Signed: signed}, nil
}
