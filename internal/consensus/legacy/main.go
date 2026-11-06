// Command legacy serves consensus as a StandardCapabilities LOOP, for nodes that do not run it with capability.Run
// yet. Its oracle is made by the node's oracle factory, as it was before consensus moved to capability.Run.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/capabilities/internal/consensus/metrics"
	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
	"github.com/smartcontractkit/capabilities/internal/consensus/service"
	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/migration"
	"github.com/smartcontractkit/capabilities/libs/ocr/legacyocr"

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

	return service.NewLegacyServer(s.Logger, clockwork.NewRealClock(), s.LimitsFactory, cfg, legacyocr.NewOracle[plugin.Info](deps.OracleFactory))
}
