package main

import (
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.Serve(trigger.ServiceName, func(lggr logger.Logger) loop.StandardCapabilities {

		triggerService, err := trigger.NewTriggerService(lggr, nil)
		if err != nil {
			lggr.Fatalw("Failed to create cron trigger service", "error", err)
		}

		return server.NewCronServer(triggerService)
	})
}
