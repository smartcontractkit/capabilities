package main

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/capabilities/internal/consensus/metrics"
	"github.com/smartcontractkit/capabilities/internal/consensus/service"
	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/migration"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// serviceName is what the LOOP was named before consensus moved to capability.Run.
const serviceName = "ConsensusCapability"

func main() {
	migration.Serve(serviceName, newConsensus, loop.WithOtelViews(metrics.MetricViews()))
}

func newConsensus(s *loop.Server, deps core.StandardCapabilitiesDependencies) (capability.Capability, error) {
	var cfg service.Config
	if deps.Config != "" {
		if err := json.Unmarshal([]byte(deps.Config), &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %s %w", deps.Config, err)
		}
	}

	if deps.OracleFactory == nil {
		return nil, errors.New("the node gives no oracle factory")
	}

	return service.NewServer(s.Logger, clockwork.NewRealClock(), s.LimitsFactory, cfg, newOracle(deps.OracleFactory))
}
