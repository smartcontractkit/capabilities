package main

import (
	"github.com/smartcontractkit/capabilities/http_trigger/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	loopserver.Serve(trigger.ServiceName, func(lggr logger.Logger) loop.StandardCapabilities {
		return server.NewHTTPServer(trigger.NewService(lggr))
	})
}
