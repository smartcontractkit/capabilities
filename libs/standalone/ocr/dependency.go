// Package ocr provides the standalone.BootstrapDependency a binary uses to obtain the libocr rage
// networking factories (OCR endpoint, OCR3.1 endpoint, and DON-to-DON peer group).
//
// There are two, mirroring the two halves of core's SingletonPeerWrapper, and a binary picks one
// by calling its constructor:
//
//   - Host: create a local libocr peer (networking.NewPeer) and expose its factories. Unlocks the
//     node's P2P identity from the keystore in the database it shares with the node, and uses
//     --ocr.listen-addresses and the OCR discoverer table in that same database.
//   - Proxy: delegate rage networking to an out-of-process host at --ocr.proxy-address, exposing
//     proxy-client-backed factories instead of a local peer. Needs no database and no keystore
//     password: it is told the peer ID the proxy hosts for it.
//
// Which of the two a binary is, is a property of the binary rather than a setting: a p2p proxy
// server hosts a peer, and a process fronted by one delegates to it. Expressing that by
// construction rather than by a mode flag means neither ever has to reject the other's settings,
// the settings a binary does have are the ones that apply to it, and the keystore password stays
// with the one process that has any use for it.
//
// Embedding replaces both with the same thing: the in-process transport (see inproc.go) under an
// identity derived from the instance index (see keyring.go and embed.go). Instances sharing a
// process have no network between them, and no node keystore to borrow an identity from, so neither
// form's settings apply and neither is required.
package ocr

import (
	"io"

	"github.com/smartcontractkit/libocr/commontypes"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"
)

// Factories is what every form of this dependency resolves, and the only part of it both kinds of
// caller need: the transports, and the identity behind them.
//
// It is a type of its own so that the two kinds do not have to share the rest. A process hosting a
// peer serves rage networking to others and signs on their behalf; a process delegating to one
// drives an oracle over that networking. Neither has any use for the other's half, and folding both
// into one struct meant every caller was handed fields that were nil for the form it had - which is
// not a shape a caller can read.
//
// Close tears down the underlying peer (a hosted one) or the proxy client connections (a delegating
// one), and nothing at all for an embedded instance, which holds neither.
type Factories struct {
	// OCR2Endpoint creates OCR2 BinaryNetworkEndpoints.
	OCR2Endpoint ocr2types.BinaryNetworkEndpointFactory
	// OCR3_1Endpoint creates OCR3.1 BinaryNetworkEndpoint2s.
	OCR3_1Endpoint ocr2types.BinaryNetworkEndpoint2Factory

	// PeerID is the node's rage P2P identity: unlocked from the keystore, configured directly, or
	// derived from the instance index. Every form resolves it, and consumers other than libocr need
	// it: the on-chain CapabilitiesRegistry keys node records by peer ID, so anything reading
	// registry metadata must know which node it is.
	PeerID ragetypes.PeerID

	closer io.Closer
}

// NewFactories returns the transport a peer or a proxy provides, with closer as what Close releases -
// the peer itself, or the connections to the process hosting it.
//
// Exported for the hosting form, which lives in libs/standalone/rage: it builds a real peer and has
// to hand back this same shape, and what Close releases is not something a caller should be able to
// forget to set.
func NewFactories(
	ocr2 ocr2types.BinaryNetworkEndpointFactory,
	ocr31 ocr2types.BinaryNetworkEndpoint2Factory,
	peerID ragetypes.PeerID,
	closer io.Closer,
) Factories {
	return Factories{OCR2Endpoint: ocr2, OCR3_1Endpoint: ocr31, PeerID: peerID, closer: closer}
}

// Close releases the underlying peer or proxy clients.
func (f *Factories) Close() error {
	if f == nil || f.closer == nil {
		return nil
	}
	return f.closer.Close()
}

// OCRFactories is Factories plus everything else running an oracle takes: the identity a
// configuration lists it under, the keys it signs with, the peers to dial before it has heard of
// anyone, and the configuration itself.
//
// All of it is the node's rather than the capability's, which is why it is resolved here: a
// capability is told what it runs as. What it runs *under* - the OCR configuration - is a registry
// question and comes from there instead (see libs/standalone/capability), because whoever read the
// registry is the only one that can say.
type OCRFactories struct {
	Factories

	// TransmitAccount is the account the node is registered to transmit from, the third part of the
	// identity an OCR configuration lists after the peer ID and the public keys. Resolved with them
	// because it is the same identity seen from a third side, and an oracle that has two of the
	// three is one libocr does not recognise.
	//
	// Empty for a process that runs no oracle and so has no account to report.
	TransmitAccount ocr2types.Account

	// Keyrings sign this oracle's protocol messages and its reports. Resolved with the networking
	// because they come from the same place: whoever holds the node's identity holds both - or, for
	// an embedded instance, derives both from its index.
	Keyrings

	// Bootstrappers are the peers to dial before this oracle has heard of anyone. A configuration
	// says who the DON is, not where to find it, so this is configured alongside the networking it
	// is dialled over - and not by the capability, which would be answering a question about a
	// network it is deliberately kept away from.
	//
	// Empty for an embedded run: its peers are goroutines in this process, so there is nothing to
	// dial and nothing to be told.
	Bootstrappers []commontypes.BootstrapperLocator
}

// multiCloser closes several io.Closers, returning the first error.
type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var err error
	for _, c := range m {
		if cerr := c.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}
