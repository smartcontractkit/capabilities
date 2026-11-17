package service

import (
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
	"github.com/smartcontractkit/capabilities/internal/consensus/protos"
	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/ocr"

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

	oracles, err := do.Invoke[ocr.Oracles](i)
	if err != nil {
		return nil, err
	}

	return NewServer(lggr, clock, limitsFactory, *cfg, oracles.New[plugin.ReportInfo])
}

// NewServer builds the consensus capability, which makes its oracle with newOracle.
func NewServer(lggr logger.Logger, clock clockwork.Clock, limitsFactory limits.Factory, cfg Config,
	newOracle ocr.NewOracle[plugin.ReportInfo]) (*protos.ConsensusServer, error) {
	c, err := newConsensusCapability(lggr, clock, limitsFactory, cfg, newOracle)
	if err != nil {
		return nil, err
	}

	return protos.NewConsensusServer(c), nil
}
