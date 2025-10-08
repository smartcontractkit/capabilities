package main

import (
	"github.com/smartcontractkit/capabilities/capabilitywatcher/server"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	loopserver.ServeNew("CapabilityWatcher", func(s *loop.Server) loop.StandardCapabilities {
		return server.New(s.Logger)
	})
}
