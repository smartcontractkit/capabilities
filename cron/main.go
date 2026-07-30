package main

import (
	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

func main() {
	loopserver.ServeNew(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		// Server.MeteringConfig is the single, canonical loop-env -> metering
		// mapping (enable flags, snapshot interval, deployment identity); no
		// per-main copy of that mapping, and no reaching for a process-global
		// emitter (it injects the server's own durable emitter).
		metering := s.MeteringConfig()
		meters := resourcemanager.NewResourceManager(s.Logger, metering.ResourceManagerConfig)

		triggerService, err := trigger.NewTriggerService(s.Logger, nil, s.LimitsFactory, meters)
		if err != nil {
			s.Logger.Fatalw("Failed to create cron trigger service", "error", err)
		}
		triggerService.Deployment = metering.DeploymentIdentity

		return server.NewCronServer(triggerService)
	}, loop.WithOtelViews(trigger.MetricViews()))
}
