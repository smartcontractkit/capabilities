package rage

import (
	"context"

	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"

	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// embedded is one embedded instance's stand-in for a hosted peer: the in-process transport, and the
// OCR key bundle this process would have signed with on other oracles' behalf.
//
// There is no peer here to host. The instances an embedded run talks to are goroutines beside it, so
// there is nothing to listen on, announce to or discover, and no keystore to unlock an identity from -
// the identity comes from the instance index instead (see ocr.EmbeddedFactories). Which is why this
// form has no settings at all: everything the hosting form is configured with, an embedded instance
// either derives or has no use for.
type embedded struct {
	lggr  commonlogger.Logger
	index int
}

var _ standalone.BootstrapDependency[*Factories] = (*embedded)(nil)

// Namespace is the same ocr.* the hosting form uses, though it names nothing: there is no
// configuration here to root under it.
func (d *embedded) Namespace() string { return "ocr" }

// Config is nil: there is nothing to configure. Which is the point - an embedded instance cannot be
// told a listen address or a keystore password, so `embed` does not offer them.
func (d *embedded) Config() any { return nil }

func (d *embedded) Dependencies() []standalone.BootstrapCommand {
	// No database: the identity is derived, and there are no announcements to store when every peer
	// is in this process.
	return []standalone.BootstrapCommand{}
}

// ForEmbedding returns the dependency of instance i, so an already-embedded dependency embedded
// again is that instance's rather than a nesting of them. How many instances there are does not
// matter here: hosting a peer is about a network, and this instance's transport is the same whoever
// else is running.
func (d *embedded) ForEmbedding(i, _ int) standalone.BootstrapDependency[*Factories] {
	return &embedded{lggr: d.lggr, index: i}
}

// Get resolves the transport and this instance's bundle.
//
// PeerGroup and Keyring are left unset: there is no in-process peer group simulation, so an embedded
// instance cannot host don2don.Dispatcher.
func (d *embedded) Get(context.Context, standalone.CommonConfig) (*Factories, error) {
	factories, err := ocr.EmbeddedFactories(d.lggr, d.index)
	if err != nil {
		return nil, err
	}

	// The bundle a hosted peer would have taken from the node's keystore. Serving it is all this form
	// can still do with it - an oracle in this process holds its own, derived the same way.
	bundle, err := ocr.EmbeddedOCR2Bundle(d.index)
	if err != nil {
		return nil, err
	}

	return &Factories{Factories: factories, OCR2: bundle}, nil
}
