package main

import (
	"github.com/smartcontractkit/capabilities/http_action/action"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	loopserver.ServeNew(action.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		return server.NewClientServer(action.NewService(s.Logger, s.LimitsFactory))
	})
}
