package main

import (
	"github.com/smartcontractkit/capabilities/http_trigger/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

func newMeteringConfig(env loop.EnvConfig) trigger.MeteringConfig {
	return trigger.MeteringConfig{
		MeterRecordsEnabled:   env.MeterRecordsEnabled,
		MeterSnapshotsEnabled: env.MeterSnapshotsEnabled,
		Deployment: resourcemanager.DeploymentIdentity{
			Product:         env.MeterProduct,
			Tenant:          env.MeterTenant,
			NumericTenantID: env.MeterNumericTenantID,
			Environment:     env.MeterEnvironment,
			Zone:            env.MeterZone,
			NodeID:          env.MeterNodeID,
		},
	}
}

func main() {
	loopserver.ServeNew(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		meteringCfg := newMeteringConfig(s.EnvConfig)
		svc := trigger.NewService(s.Logger, s.LimitsFactory, meteringCfg)
		return server.NewHTTPServer(svc)
	})
}
