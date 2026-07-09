package main

import (
	"github.com/smartcontractkit/capabilities/http_trigger/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

func main() {
	loopserver.ServeNew(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		// ConfigFromEnv is the single, canonical loop-env -> metering mapping
		// (enable flags, beholder emitter, snapshot interval, deployment
		// identity); no per-main copy of that mapping.
		meteringCfg := resourcemanager.ConfigFromEnv(&s.EnvConfig)
		svc := trigger.NewService(s.Logger, s.LimitsFactory, meteringCfg)
		return server.NewHTTPServer(svc)
	})
}
