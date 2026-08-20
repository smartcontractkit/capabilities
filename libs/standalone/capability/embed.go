package capability

import (
	"context"
	"fmt"

	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"

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
	lggr    logger.Logger
	servers common.BootstrapDependency[*standalonegrpc.Factory]

	// instances is how many the run has, which is the DON an OCR-based capability here runs in.
	// Nothing else about embedding needs it, and no instance could work it out for itself.
	instances int

	// cfg is shared with the configured form that produced this one, and with
	// every other instance's form, so all of them read the settings the flags were
	// bound to rather than a copy of the defaults.
	cfg *embeddedConfig
}

var _ common.BootstrapDependency[Dependencies] = (*embedded)(nil)

func (d *embedded) Namespace() string { return "capabilities" }

func (d *embedded) Config() any { return d.cfg }

func (d *embedded) Dependencies() []common.BootstrapCommand {
	return []common.BootstrapCommand{d.servers}
}

// ForEmbedding returns instance i's form, so an already-embedded dependency
// embedded again is that instance's rather than a nesting of them. The settings
// are carried over by pointer, so every instance still reads the ones the flags
// were bound to.
//
// The gRPC factory is embedded in turn rather than reused as-is: how it serves
// instance i is its own business, and this only has to ask it the same question
// the configured form did.
func (d *embedded) ForEmbedding(i, instances int) common.BootstrapDependency[Dependencies] {
	return &embedded{lggr: d.lggr, servers: d.servers.ForEmbedding(i, instances), instances: instances, cfg: d.cfg}
}

func (d *embedded) Get(ctx context.Context, cc common.CommonConfig) (Dependencies, error) {
	// Read once into a copy: the settings are shared with every other instance's form, and what an
	// instance resolves itself from should not change halfway through - nor be something it could
	// write back to its siblings.
	cfg := *d.cfg

	settings, err := newSettings(d.lggr)
	if err != nil {
		return Dependencies{}, err
	}

	// The one factory every instance resolves, so the capabilities of a whole
	// embed run take consecutive ports rather than every instance starting over
	// at the same one.
	servers, err := d.servers.Get(ctx, cc)
	if err != nil {
		return Dependencies{}, fmt.Errorf("failed to get gRPC server factory: %w", err)
	}

	d.lggr.Infow("Using in-process capability registry", "donID", cfg.CapabilityDonID, "instances", d.instances)

	// No proxy: only what this binary registers is resolvable, and the registry
	// metadata calls say so rather than inventing a DON. The capabilities are
	// still served - see registrar.serve.
	return Dependencies{
		LimitsFactory:      newLimitsFactory(d.lggr, settings),
		CRESettings:        settings,
		CapabilityRegistry: registry.Local(d.lggr),
		// Computed from the run rather than read from a node, and arriving on the same field it would
		// have arrived on either way - see RegisterEmbeddedOCRConfig.
		OCRConfigRegistry: embeddedOCRConfigRegistry(d.instances),
		CapabilityDonID:   cfg.CapabilityDonID,
		lggr:              d.lggr,
		settings:          settings,
		settingsPath:      SettingsPath(),
		servers:           servers,
	}, nil
}
