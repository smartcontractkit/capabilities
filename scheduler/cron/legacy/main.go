// Command legacy serves cron as a StandardCapabilities LOOP, for nodes that do not run it with capability.Run yet.
package main

import (
	"encoding/json"
	"fmt"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/migration"
	"github.com/smartcontractkit/capabilities/scheduler/cron/protos"
	"github.com/smartcontractkit/capabilities/scheduler/cron/service"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// serviceName is what the LOOP was named before cron moved to capability.Run.
const serviceName = "CronCapabilities"

func main() {
	migration.Serve(serviceName, newCron, loop.WithOtelViews(service.MetricViews()))
}

func newCron(s *loop.Server, deps core.StandardCapabilitiesDependencies) (capability.Capability, error) {
	var cfg service.Config
	if deps.Config != "" {
		if err := json.Unmarshal([]byte(deps.Config), &cfg); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %s %w", deps.Config, err)
		}
	}

	svc, err := service.NewService(s.Logger, clockwork.NewRealClock(), s.LimitsFactory, cfg, deps.OrgResolver, deps.ShardIndex)
	if err != nil {
		return nil, err
	}

	return protos.NewCronServer(svc), nil
}
