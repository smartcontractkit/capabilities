package ocr

import (
	"context"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

// embedded is one embedded instance's rage networking: an identity derived from the instance index
// and the in-process transport (see inproc.go and keyring.go).
//
// It is what both Host and Proxy return from ForEmbedding, because embedding erases the difference
// between them. Hosting a peer or delegating to one is a question about a network, and there is no
// network here: the peers an embedded instance talks to are goroutines in the same process, reached
// over channels. Delegating that to a proxy would mean serialising a message so a gRPC connection to
// this process could hand it back.
//
// So this needs no configuration at all: no listen address, no proxy address, no peer ID, and above
// all no keystore password, since the identity is derived rather than unlocked. That is why the
// settings those forms do need are checked when they are resolved rather than tagged `required` -
// an embedded instance would have to be given values it has no use for.
type embedded struct {
	lggr  commonlogger.Logger
	index int
}

var _ standalone.BootstrapDependency[*RageFactories] = (*embedded)(nil)

// Namespace is the same ocr.* the configured forms use, though it names nothing: there is no
// configuration here to root under it.
func (d *embedded) Namespace() string { return "ocr" }

// Config is nil: there is nothing to configure. Which is the point - an embedded instance cannot be
// told a listen address, a proxy address, a peer ID or a keystore password, so `embed` does not
// offer them.
func (d *embedded) Config() any { return nil }

func (d *embedded) Dependencies() []standalone.BootstrapCommand {
	// No database: the identity is derived, and there are no announcements to store when every peer
	// is in this process.
	return []standalone.BootstrapCommand{}
}

// ForEmbedding returns the dependency of instance i, so an already-embedded dependency embedded
// again is that instance's rather than a nesting of them.
func (d *embedded) ForEmbedding(i int) standalone.BootstrapDependency[*RageFactories] {
	return &embedded{lggr: d.lggr, index: i}
}

func (d *embedded) Get(context.Context, standalone.CommonConfig) (*RageFactories, error) {
	keyring, err := DeterministicKeyring(d.index)
	if err != nil {
		return nil, err
	}
	peerID := ragetypes.PeerIDFromKeyring(keyring)

	d.lggr.Infow("Using in-process rage networking",
		"instance", d.index,
		"peerID", peerID.String(),
	)

	// Nothing here is closed: the endpoints are closed by whoever created them, and the transport
	// itself holds no resource beyond the maps they register in. PeerGroup and Keyring are left
	// unset: there is no in-process peer group simulation, so an embedded instance cannot host
	// don2don.Dispatcher.
	return &RageFactories{
		OCRFactories: OCRFactories{
			OCR2Endpoint:   ocr2Factory{net: embedNetwork, peerID: peerID.String(), bufferSize: defaultBufferSize},
			OCR3_1Endpoint: ocr31Factory{net: embedNetwork, peerID: peerID.String(), bufferSize: defaultBufferSize},
			PeerID:         peerID,
		},
	}, nil
}

// embeddedOCR adapts embedded to standalone.BootstrapDependency[*OCRFactories], for Proxy's
// ForEmbedding: a delegating process only ever needs the OCR half.
type embeddedOCR struct {
	*embedded
}

var _ standalone.BootstrapDependency[*OCRFactories] = embeddedOCR{}

func (d embeddedOCR) ForEmbedding(i int) standalone.BootstrapDependency[*OCRFactories] {
	return embeddedOCR{&embedded{lggr: d.lggr, index: i}}
}

func (d embeddedOCR) Get(ctx context.Context, cc standalone.CommonConfig) (*OCRFactories, error) {
	f, err := d.embedded.Get(ctx, cc)
	if err != nil {
		return nil, err
	}
	return &f.OCRFactories, nil
}
