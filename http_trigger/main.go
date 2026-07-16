package main

import (
	"github.com/smartcontractkit/capabilities/http_trigger/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/durableemitter"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

func main() {
	loopserver.ServeNew(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		// EnvConfig.MeteringConfig is the single, canonical loop-env -> metering
		// mapping (enable flags, snapshot interval, deployment identity); no
		// per-main copy of that mapping. The durable emitter is resolved here
		// and injected, since resourcemanager itself must not reach for the
		// process-global emitter.
		var emitter resourcemanager.Emitter
		if de := durableemitter.GetGlobalEmitter(); de != nil {
			emitter = de
		}
		meteringCfg := s.EnvConfig.MeteringConfig(emitter)
		svc := trigger.NewService(s.Logger, s.LimitsFactory, meteringCfg)
		return server.NewHTTPServer(svc)
	})
}
