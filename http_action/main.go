package main

import (
	"github.com/smartcontractkit/capabilities/http_action/action"
	"github.com/smartcontractkit/capabilities/http_action/pb"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	loopserver.Serve(action.ServiceName, func(lggr logger.Logger) loop.StandardCapabilities {
		return pb.NewClientServer(action.NewService(lggr))
	})
}
