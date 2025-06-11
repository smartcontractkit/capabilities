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
		return server.NewCronServer(trigger.NewTriggerService(lggr, nil))
	})
}
