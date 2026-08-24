package ocr

import (
	"context"
	"fmt"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// EmbeddedFactories is the transport of instance i of an embedded run: an identity derived from the
// index, over the in-process network (see inproc.go and keyring.go).
//
// It is what embedding replaces both configured forms with, because embedding erases the difference
// between them. Hosting a peer or delegating to one is a question about a network, and there is no
// network here: the peers an embedded instance talks to are goroutines in the same process, reached
// over channels. Delegating that to a proxy would mean serialising a message so a gRPC connection to
// this process could hand it back.
//
// Exported for the hosting form, which is in another package (libs/standalone/rage) and resolves the
// same transport for an embedded run.
//
// Nothing in it needs closing: the endpoints are closed by whoever created them, and the transport
// itself holds no resource beyond the maps they register in.
func EmbeddedFactories(lggr commonlogger.Logger, i int) (Factories, error) {
	keyring, err := DeterministicKeyring(i)
	if err != nil {
		return Factories{}, err
	}
	peerID := ragetypes.PeerIDFromKeyring(keyring)

	lggr.Infow("Using in-process rage networking", "instance", i, "peerID", peerID.String())

	return Factories{
		OCR2Endpoint:   ocr2Factory{net: embedNetwork, peerID: peerID.String(), bufferSize: defaultBufferSize},
		OCR3_1Endpoint: ocr31Factory{net: embedNetwork, peerID: peerID.String(), bufferSize: defaultBufferSize},
		PeerID:         peerID,
	}, nil
}

// embedded is one embedded instance's OCR dependency: the in-process transport, the keys it signs
// with, and the account the configuration of this run lists it under.
//
// So it needs almost no configuration: no listen address, no proxy address, no peer ID, no account,
// no bootstrap addresses, and above all no keystore password, since the identity is derived rather
// than unlocked. That is why the settings the configured forms do need are checked when they are
// resolved rather than tagged `required` - an embedded instance would have to be given values it has
// no use for. What is left is the protocol itself: see embeddedOCRConfig.
type embedded struct {
	lggr  commonlogger.Logger
	index int

	// instances is how many there are, which is the DON these oracles form. Kept only to check this
	// instance is in it: an index outside the DON is a run whose --instances disagrees with itself,
	// and saying so beats spending the run as a member no configuration lists.
	instances int
}

var _ standalone.BootstrapDependency[*OCRFactories] = (*embedded)(nil)

// Namespace is the same ocr.* the configured forms use: an embedded instance is still an oracle, and
// what little it is configured with is the protocol it runs.
func (d *embedded) Namespace() string { return "ocr" }

// Config is the protocol these oracles run, defaulted: the real shared configuration, so anything
// libocr can be told about a round is a flag here too. Who the members are and what the digest is
// are not in it - see EmbeddedOCRConfig, which fills both in.
func (d *embedded) Config() any { return &DefaultEmbeddedOCRConfig }

func (d *embedded) Dependencies() []standalone.BootstrapCommand {
	// No database: the identity is derived, and there are no announcements to store when every peer
	// is in this process.
	return []standalone.BootstrapCommand{}
}

// ForEmbedding returns the dependency of instance i, so an already-embedded dependency embedded
// again is that instance's rather than a nesting of them.
func (d *embedded) ForEmbedding(i, instances int) standalone.BootstrapDependency[*OCRFactories] {
	return &embedded{lggr: d.lggr, index: i, instances: instances}
}

func (d *embedded) Get(context.Context, standalone.CommonConfig) (*OCRFactories, error) {
	if d.index >= d.instances {
		return nil, fmt.Errorf("instance %d is not one of the %d instances of this run, so no configuration this run computes will list it",
			d.index, d.instances)
	}

	factories, err := EmbeddedFactories(d.lggr, d.index)
	if err != nil {
		return nil, err
	}

	// Its own bundle, for the same reason it derives its own peer identity: there is no node keystore
	// behind an embedded instance and so nothing to sign on its behalf. Which also means it signs
	// here rather than asking a proxy - what the proxy form does is reach a key it does not have.
	keyrings, err := EmbeddedKeyrings(d.index)
	if err != nil {
		return nil, err
	}
	bundle, err := EmbeddedOCR2Bundle(d.index)
	if err != nil {
		return nil, err
	}

	return &OCRFactories{
		Factories: factories,
		// Unlike a delegating run, an embedded instance does have an account of its own to report:
		// the configuration it joins is the one built over this process's instances (see
		// EmbeddedOCRConfig), which lists it under exactly this account.
		TransmitAccount: EmbeddedTransmitAccount(bundle),
		Keyrings:        keyrings,
		// Bootstrappers stays empty: an embedded instance's peers are goroutines beside it, so there
		// is nothing to dial and no address anyone could be told.
	}, nil
}
