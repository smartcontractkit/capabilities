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
	"time"

	"github.com/smartcontractkit/libocr/networking"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/ocrcommon"
)

// OCRFactories bundles the libocr OCR networking factories the caller serves or drives. Any of the
// forms above produces it; the caller does not need to know which. Close tears down the underlying
// peer (a hosted one) or the proxy client connections (a delegating one), and nothing at all for an
// embedded instance, which holds neither.
type OCRFactories struct {
	// OCR2Endpoint creates OCR2 BinaryNetworkEndpoints.
	OCR2Endpoint ocr2types.BinaryNetworkEndpointFactory
	// OCR3_1Endpoint creates OCR3.1 BinaryNetworkEndpoint2s.
	OCR3_1Endpoint ocr2types.BinaryNetworkEndpoint2Factory

	// PeerID is the node's rage P2P identity: unlocked from the keystore, configured directly, or
	// derived from the instance index. Every form resolves it, and consumers other than libocr need
	// it: the on-chain CapabilitiesRegistry keys node records by peer ID, so anything reading
	// registry metadata must know which node it is.
	PeerID ragetypes.PeerID

	// Keyrings sign this oracle's protocol messages and its reports. Resolved with the networking
	// because they come from the same place: whoever holds the node's identity holds both.
	Keyrings

	closer io.Closer
}

// Close releases the underlying peer or proxy clients.
func (f *OCRFactories) Close() error {
	if f == nil || f.closer == nil {
		return nil
	}
	return f.closer.Close()
}

// RageFactories is OCRFactories plus what only a real, locally hosted peer can give: a factory for
// DON-to-DON peer groups over that same rage connection, and the keyring to sign with the same key
// it uses. don2don.Dispatcher takes both.
type RageFactories struct {
	OCRFactories

	// PeerGroup creates DON-to-DON peer groups.
	PeerGroup networking.PeerGroupFactory
	// Keyring signs with the same P2P key the peer above uses, for don2don.Dispatcher's
	// message-level signatures.
	Keyring ragetypes.PeerKeyring

	// OCR2 is the node's OCR key bundle, which this process signs with on behalf of oracles that
	// hold no keys of their own. Nil for an embedded run, which has no node keystore to take one
	// from.
	OCR2 ocr2key.KeyBundle
}

// defaultPeerConfig is the peer settings the flags are bound to and decoded into, so an unset
// setting keeps the value it is given here. Freshly allocated per call rather than shared, so two
// dependencies (or two instances of one) can never decode into the same peer settings.
func defaultPeerConfig() *ocrcommon.Config {
	return &ocrcommon.Config{
		DeltaReconcile:     *config.MustNewDuration(time.Minute),
		DeltaDial:          *config.MustNewDuration(5 * time.Second),
		IncomingBufferSize: 100,
		OutgoingBufferSize: 100,
	}
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
