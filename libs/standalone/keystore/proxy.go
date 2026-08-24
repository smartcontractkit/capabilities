// Package keystore is how a capability signs with keys it does not hold.
//
// A capability binary runs beside the node rather than inside it, and the keys
// that say who the node is stay with the node - or, in this framework, with the
// process fronting it. What a capability gets instead is this: the signing, over
// gRPC, of digests it computed itself, by an account the on-chain registry
// already knows this node by.
//
// It resolves to chainlink-common's core.Keystore, which is what everything
// signing for a chain is written against - chainlink-evm's transaction manager
// included - so what a capability does with it is hand it over unchanged.
package keystore

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// namespace groups this dependency's settings under keystore.*.
//
// Its own rather than shared with ocr.*: the two happen to be served by one
// process today, but what they ask for differs - one is the identity a protocol
// runs under, the other is the account a chain is written to - and a deployment
// that ever splits them would need to say so with two addresses.
const namespace = "keystore"

// Config is where the keys are.
//
// Not `validate:"required"`: an embedded instance signs with keys derived from
// its index and has no address to dial, so the rule is checked when this form is
// resolved rather than tagged on a field both forms share.
type Config struct {
	ProxyAddress string `usage:"gRPC address of the process holding this node's chain keys, which signs on this capability's behalf; required outside embed"`
}

// Proxy returns the standalone.BootstrapDependency a capability resolves to sign
// as the node it runs beside.
//
// Resolving it dials the holder and asks which accounts it has. That call is the
// point: a capability configured to transmit from an account the node does not
// hold is misconfigured, and finding that out while starting up is better than
// finding it out from the first transaction that needed a signature.
//
// embedded is what an embedded run signs with instead, since it has no node to
// borrow from; nil says this binary cannot run embedded. See Embedded.
func Proxy(lggr commonlogger.Logger, embedded Embedded) standalone.BootstrapDependency[core.Keystore] {
	// Wrapped so the connection is dialled at most once however many services resolve this.
	return standalone.OnceBootstrapper[core.Keystore](&proxyDependency{lggr: lggr, embedded: embedded})
}

type proxyDependency struct {
	lggr     commonlogger.Logger
	embedded Embedded
	cfg      Config
}

var _ standalone.BootstrapDependency[core.Keystore] = (*proxyDependency)(nil)

func (d *proxyDependency) Namespace() string { return namespace }

func (d *proxyDependency) Config() any { return &d.cfg }

func (d *proxyDependency) Dependencies() []standalone.BootstrapCommand { return nil }

// ForEmbedding returns the in-process form: an embedded run has no node to borrow
// an account from, so instance i signs with keys of its own. See embedded.
func (d *proxyDependency) ForEmbedding(i, _ int) standalone.BootstrapDependency[core.Keystore] {
	return &embedded{lggr: d.lggr, build: d.embedded, index: i}
}

func (d *proxyDependency) Get(ctx context.Context, _ standalone.CommonConfig) (core.Keystore, error) {
	if d.cfg.ProxyAddress == "" {
		return nil, errors.New("--keystore.proxy-address is required to sign with the node's keys")
	}

	// grpc.NewClient does not connect: the Accounts call below is what finds out
	// whether anything is there, which is why it is made here rather than left to
	// the first signature.
	conn, err := grpc.NewClient(d.cfg.ProxyAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("failed to create keystore client for %s: %w", d.cfg.ProxyAddress, err)
	}

	remote := &remoteKeystore{client: creproxy.NewKeystoreClient(conn), closer: conn.Close}

	accounts, err := remote.Accounts(ctx)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("failed to read the accounts held at %s: %w", d.cfg.ProxyAddress, err)
	}

	d.lggr.Infow("Signing with the node's keys", "keystoreAddress", d.cfg.ProxyAddress, "accounts", accounts)

	return remote, nil
}

// remoteKeystore is core.Keystore over the connection to whoever holds the keys.
type remoteKeystore struct {
	client creproxy.KeystoreClient
	closer func() error
}

var _ core.Keystore = (*remoteKeystore)(nil)

func (k *remoteKeystore) Accounts(ctx context.Context) ([]string, error) {
	reply, err := k.client.Accounts(ctx, &creproxy.AccountsRequest{})
	if err != nil {
		return nil, err
	}
	return reply.GetAccounts(), nil
}

func (k *remoteKeystore) Sign(ctx context.Context, account string, data []byte) ([]byte, error) {
	reply, err := k.client.Sign(ctx, &creproxy.SignRequest{Account: account, Data: data})
	if err != nil {
		return nil, err
	}
	return reply.GetSigned(), nil
}

// Decrypt is not served: the keys lent out here sign, and a capability asking to
// decrypt with one is asking for something the holder will not do either.
func (k *remoteKeystore) Decrypt(context.Context, string, []byte) ([]byte, error) {
	return nil, errors.New("the node's chain keys sign; they do not decrypt")
}

// Close releases the connection. The bootstrapper closes resolved dependency
// values on shutdown, after the services built from them.
func (k *remoteKeystore) Close() error { return k.closer() }
