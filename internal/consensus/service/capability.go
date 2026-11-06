package service

import (
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"

	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/libs/capability"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr/remote"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

// ConfigNamespace is the namespace the consensus capability's config is set under.
const ConfigNamespace = "consensus"

// ResponseCacheExpiry is how long a response is kept for requests sent after consensus is reached.
const ResponseCacheExpiry = time.Minute

// init provides the config however the capability is run, so whoever embeds it gets its defaults without providing
// it.
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

	deps, err := do.Invoke[remote.Dependencies](i)
	if err != nil {
		return nil, err
	}

	return NewServer(lggr, clock, limitsFactory, *cfg, deps)
}

// NewServer builds the consensus capability, its oracle running over deps.
func NewServer(lggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory, cfg Config,
	deps remote.Dependencies) (*protos.ConsensusServer, error) {
	c, err := newConsensusCapability(lggr, clock, ResponseCacheExpiry, limitsFactory)
	if err != nil {
		return nil, err
	}

	if err = c.Configure(cfg, deps); err != nil {
		return nil, err
	}

	return protos.NewConsensusServer(c), nil
}

// NewLegacyServer builds the consensus capability for a node that makes its oracle with newOracle, such as
// legacyocr.NewOracle. The node fills in the oracle's parts that are its own.
func NewLegacyServer(lggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory, cfg Config,
	newOracle func(libocr.OracleArgs) (libocr.Oracle, error)) (*protos.ConsensusServer, error) {
	c, err := newConsensusCapability(lggr, clock, ResponseCacheExpiry, limitsFactory)
	if err != nil {
		return nil, err
	}

	if err = c.Configure(cfg, remote.Dependencies{}); err != nil {
		return nil, err
	}

	c.newOracle = newOracle
	return protos.NewConsensusServer(c), nil
}
