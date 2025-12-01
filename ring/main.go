package main

import (
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/ring/server"
)

func main() {
	loopserver.ServeNew("RingCapability", func(s *loop.Server) loop.StandardCapabilities {
		requestTimeout := 5 * time.Minute
		timeToSync := 30 * time.Second
		f := 3 // fault tolerance parameter

		return server.New(s.Logger, requestTimeout, timeToSync, f)
	})
}

