package main

import (
	"github.com/smartcontractkit/capabilities/http_trigger/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

func main() {
	loopserver.ServeNew(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		// Server.MeteringConfig is the single, canonical loop-env -> metering
		// mapping (enable flags, snapshot interval, deployment identity); no
		// per-main copy of that mapping, and no reaching for a process-global
		// emitter (it injects the server's own durable emitter).
		svc := trigger.NewService(s.Logger, s.LimitsFactory, s.MeteringConfig())
		return server.NewHTTPServer(svc)
	})
}
