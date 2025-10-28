package main

import (
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/mock/server"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	loopserver.ServeNew("MockCapability", func(s *loop.Server) loop.StandardCapabilities {
		return server.New(s.Logger)
	})
}
