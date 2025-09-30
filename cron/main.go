package main

import (
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.ServeNewWithOtelViews(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		triggerService, err := trigger.NewTriggerService(s.Logger, nil)
		if err != nil {
			s.Logger.Fatalw("Failed to create cron trigger service", "error", err)
		}

		return server.NewCronServer(triggerService)
	}, trigger.MetricViews())
}
