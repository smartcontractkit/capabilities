package service

import (
	"context"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"

	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/libs/capability"
	_ "github.com/smartcontractkit/capabilities/libs/capability/keystore" // provides keystoreocr.Client
	keystoreocr "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
	"github.com/smartcontractkit/capabilities/libs/capability/ocr"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

// ConfigNamespace is the namespace the consensus capability's config is set under.
const ConfigNamespace = "consensus"

// responseCacheExpiry is how long a response is kept for requests sent after consensus is reached.
const responseCacheExpiry = time.Minute

// init provides the config however the capability is run, so whoever embeds it gets the defaults without providing
// them.
func init() {
	capability.Injectors.MustProvideConfig(ConfigNamespace, &Config{})
}

var _ capability.Capability = (*protos.ConsensusServer)(nil)

// New builds the consensus capability from what the run provides, for capability.Run.
func New(i do.Injector) (*protos.ConsensusServer, error) {
	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return nil, err
	}

	clock, err := do.Invoke[clockwork.Clock](i)
	if err != nil {
		return nil, err
	}

	limitsFactory, err := do.Invoke[limits.Factory](i)
	if err != nil {
		return nil, err
	}

	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return nil, err
	}

	newOracle, err := newRegistryOracle(i)
	if err != nil {
		return nil, err
	}

	return NewServer(lggr, clock, limitsFactory, *cfg, newOracle)
}

// NewOracle makes the consensus oracle from the args the capability fills in.
type NewOracle = func(context.Context, libocr.OCR3OracleArgs2[plugin.ReportInfo]) (libocr.Oracle, error)

// newRegistryOracle makes consensus's oracle the way the node's own are made, over the node's networking, keystore and
// registry.
func newRegistryOracle(i do.Injector) (NewOracle, error) {
	network, err := do.Invoke[ocr.Network](i)
	if err != nil {
		return nil, err
	}

	keystore, err := do.Invoke[keystoreocr.Client](i)
	if err != nil {
		return nil, err
	}

	reg, err := do.Invoke[registry.CapabilitiesRegistry](i)
	if err != nil {
		return nil, err
	}

	capabilities, err := do.Invoke[capability.CapabilitiesConfig](i)
	if err != nil {
		return nil, err
	}

	return func(ctx context.Context, args libocr.OCR3OracleArgs2[plugin.ReportInfo]) (libocr.Oracle, error) {
		return ocr.NewRegistryOracle(ctx, args, network, keystore, reg, protos.ConsensusID, capabilities.CapabilityDonID)
	}, nil
}

// NewServer builds the consensus capability, which makes its oracle with newOracle.
func NewServer(lggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory, cfg Config,
	newOracle NewOracle) (*protos.ConsensusServer, error) {
	c, err := newConsensusCapability(lggr, clock, limitsFactory, cfg, newOracle)
	if err != nil {
		return nil, err
	}

	return protos.NewConsensusServer(c), nil
}
