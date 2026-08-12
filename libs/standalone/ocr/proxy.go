package ocr

import (
	"context"
	"errors"
	"fmt"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// ProxyConfig is the configuration of a process that delegates its rage networking to a p2p proxy
// hosting the peer, rather than hosting one itself: the address to reach that proxy at, and the
// peer ID it hosts.
//
// The peer ID is configured directly rather than looked up. A delegating process does need to know
// it - libocr compares it against the peer IDs in the OCR config, and the on-chain
// CapabilitiesRegistry keys node records by it - but it is a public value, and the only way to
// obtain it from the keystore is to hold the password that unlocks the keystore's private keys.
// Handing that password to every process that merely wants to know its own name would spread a
// secret across the deployment to no end; the process hosting the peer already has it, and is the
// only one that needs it.
//
// Neither setting is `validate:"required"`: an embedded instance resolves a dependency with no
// settings at all (see ForEmbedding), having no proxy to reach and a derived identity, so the rules
// are checked when this form is resolved instead.
type ProxyConfig struct {
	ProxyAddress string `usage:"gRPC address of the p2p proxy this process delegates rage networking to; required outside embed"`

	// PeerID is decoded from its text form by the flags package, since ragetypes.PeerID
	// unmarshals text itself - so it is validated as a peer ID when the configuration is decoded
	// rather than wherever it is first used.
	PeerID ragetypes.PeerID `usage:"this node's rage p2p peer ID, hosted on its behalf by the proxy; required outside embed" example:"'12D3KooWKh28EhBVfiiFh39w3zqtBxzYJhmGfBZNmoL4tRjMWSor'"`
}

// Proxy returns a standalone.BootstrapDependency that resolves the libocr Factories from proxy
// clients: no peer is created here, and rage networking happens in the process at
// --ocr.proxy-address, on behalf of --ocr.peer-id.
//
// It needs no database: everything a delegating process knows about its identity, it is told.
//
// An embedded instance delegates to nothing: see ForEmbedding.
func Proxy(lggr commonlogger.Logger) standalone.BootstrapDependency[*Factories] {
	// Wrap in OnceBootstrapper so Get (which dials the proxy) runs at most once even if several
	// services resolve this dependency.
	return standalone.OnceBootstrapper[*Factories](&proxyDependency{lggr: lggr})
}

type proxyDependency struct {
	lggr commonlogger.Logger

	cfg ProxyConfig
}

var _ standalone.BootstrapDependency[*Factories] = (*proxyDependency)(nil)

// Namespace groups the settings under ocr.* (--ocr.proxy-address, CRE_OCR_PROXY_ADDRESS), the same
// namespace a hosted peer's settings use: a binary is one or the other, so the names never meet.
func (d *proxyDependency) Namespace() string { return "ocr" }

func (d *proxyDependency) Config() any { return &d.cfg }

func (d *proxyDependency) Dependencies() []standalone.BootstrapCommand {
	return []standalone.BootstrapCommand{}
}

// ForEmbedding returns the in-process form, the same one a hosted peer embeds to: there is no proxy
// hop between instances of one process, since the peer this would delegate to is a goroutine beside
// it and delegating would mean serialising a message so a gRPC connection to this process could hand
// it back. None of this dependency's settings survive into it - see embedded.
func (d *proxyDependency) ForEmbedding(i int) standalone.BootstrapDependency[*Factories] {
	return &embedded{lggr: d.lggr, index: i}
}

func (d *proxyDependency) Get(context.Context, standalone.CommonConfig) (*Factories, error) {
	if d.cfg.ProxyAddress == "" {
		return nil, errors.New("--ocr.proxy-address is required to delegate rage networking")
	}
	if d.cfg.PeerID == (ragetypes.PeerID{}) {
		return nil, errors.New("--ocr.peer-id is required to delegate rage networking")
	}
	peerID := d.cfg.PeerID

	// The raw peer ID is passed to the endpoint factories, as libocr compares it against the peer
	// IDs in the OCR config.
	endpointFactory, err := creproxy.NewProxyEndpointFactory(peerID.String(), d.cfg.ProxyAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to create proxy OCR endpoint factory: %w", err)
	}
	endpoint2Factory, err := creproxy.NewProxyEndpoint2Factory(peerID.String(), d.cfg.ProxyAddress)
	if err != nil {
		_ = endpointFactory.Close()
		return nil, fmt.Errorf("failed to create proxy OCR3.1 endpoint factory: %w", err)
	}
	pgFactory, err := creproxy.NewProxyPeerGroupFactory(d.cfg.ProxyAddress)
	if err != nil {
		_ = endpointFactory.Close()
		_ = endpoint2Factory.Close()
		return nil, fmt.Errorf("failed to create proxy peer group factory: %w", err)
	}

	d.lggr.Infow("Delegating rage networking to proxy", "proxyAddress", d.cfg.ProxyAddress, "peerID", peerID.String())

	return &Factories{
		OCR2Endpoint:   endpointFactory,
		OCR3_1Endpoint: endpoint2Factory,
		PeerGroup:      pgFactory,
		PeerID:         peerID,
		closer:         multiCloser{endpointFactory, endpoint2Factory, pgFactory},
	}, nil
}
