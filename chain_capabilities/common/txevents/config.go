package txevents

import (
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/durableemitter"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Config configures ChainTransactionObserved emission. The JSON shape is shared by every
// chain capability: {"txEvents": {"enabled": true, "domain": "..."}}.
type Config struct {
	// Enabled turns emission on. The node must also run with CL_CHIP_INGRESS_DURABLE_EMITTER_ENABLED
	// for the events to leave the process; otherwise they are counted as dropped.
	Enabled bool `json:"enabled"`
	// Domain is the ChIP source domain the events are published to. Defaults to DefaultDomain.
	Domain string `json:"domain"`
}

// Normalized returns cfg with an empty domain replaced by DefaultDomain.
func (c Config) Normalized() Config {
	if c.Domain == "" {
		c.Domain = DefaultDomain
	}
	return c
}

// NewEmitter returns the emitter for cfg. Disabled config yields a NopEmitter. When emission
// is enabled but this process has no durable emitter, events are still accepted by the
// returned emitter and then dropped and counted.
func NewEmitter(lggr logger.Logger, cfg Config) (Emitter, error) {
	if !cfg.Enabled {
		return NopEmitter{}, nil
	}
	cfg = cfg.Normalized()
	if durableemitter.GetGlobalEmitter() == nil {
		lggr.Errorw("Transaction events enabled but this LOOP has no durable emitter; events will not be delivered", "domain", cfg.Domain)
	}
	emitter, err := NewDurableEmitter(lggr, cfg.Domain, GlobalSink())
	if err != nil {
		return nil, fmt.Errorf("failed to create tx events emitter: %w", err)
	}
	lggr.Infow("Transaction events enabled", "domain", cfg.Domain)
	return emitter, nil
}
