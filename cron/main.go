package main

import (
	"github.com/smartcontractkit/capabilities/cron/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"

	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

func main() {
	loopserver.Serve(trigger.ServiceName, func(lggr logger.Logger) loop.StandardCapabilities {
		return pb.NewCronServer(trigger.NewTriggerService(lggr, nil))
	})
}
