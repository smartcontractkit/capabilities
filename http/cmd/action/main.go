package main

import (
	"github.com/smartcontractkit/capabilities/http/action"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	loopserver.Serve(action.ServiceName, func(lggr logger.Logger) loop.StandardCapabilities {
		return server.NewClientServer(action.NewService(lggr))
	})
}
