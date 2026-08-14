package capability

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	common "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// defaultEmbeddedDonID is the DON an embedded instance belongs to. Embedding runs
// one DON in one process, so there is only ever the one, and requiring it to be
// configured would mean typing the same number into every embedded run.
const defaultEmbeddedDonID = 1

// embeddedConfig is the embedded form's settings. It keeps the DON ID, which an
// embedded instance can still be told, and drops the proxy port, which it cannot
// use: there is no node registry behind an embedded run to dial.
type embeddedConfig struct {
	CapabilityDonID uint32 `usage:"on-chain DON ID to report for the capabilities this process hosts"`
}

// embedded is one embedded instance's view of its host: the base registry holding
// exactly the capabilities this binary registers, and the settings and limits
// built over the same dumped file the configured form reads.
//
// The registry is the whole difference. A configured instance sits beside a node
// and asks it for anything it does not host; an embedded one has no node, so
// there is nothing behind the capabilities in this process. Resolving an ID this
// binary did not register fails, which is the honest answer - the alternative
// would be dialling a proxy that is not there.
//
// Settings are not the difference. The node still writes the file and still calls
// the reload endpoint, and an embedded run with no node simply finds no file and
// resolves every limit to its compiled-in default.
type embedded struct {
	lggr logger.Logger
	cfg  embeddedConfig
}

var _ common.BootstrapDependency[Dependencies] = (*embedded)(nil)

func (d *embedded) Namespace() string { return "capabilities" }

func (d *embedded) Config() any { return &d.cfg }

func (d *embedded) Dependencies() []common.BootstrapCommand { return nil }

// ForEmbedding returns instance i's dependency, so an already-embedded dependency
// embedded again is that instance's rather than a nesting of them.
//
// Every instance gets the same value here: instances share one process, and what
// this resolves - a registry of the capabilities in that process, and the
// settings file behind it - is the process's rather than any one instance's.
func (d *embedded) ForEmbedding(int) common.BootstrapDependency[Dependencies] { return d }

func (d *embedded) Get(context.Context, common.CommonConfig) (Dependencies, error) {
	settings, err := newSettings(d.lggr)
	if err != nil {
		return Dependencies{}, err
	}

	d.lggr.Infow("Using in-process capability registry", "donID", d.cfg.CapabilityDonID)

	// nil proxy: only what this binary registers is resolvable, and the registry
	// metadata calls report that rather than inventing a DON.
	return Dependencies{
		LimitsFactory:      newLimitsFactory(d.lggr, settings),
		CRESettings:        settings,
		CapabilityRegistry: newOverlayRegistry(d.lggr, registry.NewBaseRegistry(d.lggr), nil),
		CapabilityDonID:    d.cfg.CapabilityDonID,
		lggr:               d.lggr,
		settings:           settings,
		settingsPath:       SettingsPath(),
	}, nil
}
