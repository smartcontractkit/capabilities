package main

import (
	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

type meteringConfig struct {
	meterRecordsEnabled   bool
	meterSnapshotsEnabled bool
	deployment            resourcemanager.DeploymentIdentity
}

func newMeteringConfig(env loop.EnvConfig) meteringConfig {
	return meteringConfig{
		meterRecordsEnabled:   env.MeterRecordsEnabled,
		meterSnapshotsEnabled: env.MeterSnapshotsEnabled,
		deployment: resourcemanager.DeploymentIdentity{
			Product:         env.MeterProduct,
			Tenant:          env.MeterTenant,
			NumericTenantID: env.MeterNumericTenantID,
			Environment:     env.MeterEnvironment,
			Zone:            env.MeterZone,
			NodeID:          env.MeterNodeID,
		},
	}
}

func (m meteringConfig) resourceManagerConfig() resourcemanager.ResourceManagerConfig {
	return resourcemanager.ResourceManagerConfig{
		MeterRecordsEnabled:   m.meterRecordsEnabled,
		MeterSnapshotsEnabled: m.meterSnapshotsEnabled,
		Emitter:               beholder.GetEmitter(),
		SnapshotInterval:      resourcemanager.DefaultSnapshotInterval,
	}
}

func main() {
	loopserver.ServeNew(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		meteringCfg := newMeteringConfig(s.EnvConfig)
		meters := resourcemanager.NewResourceManager(s.Logger, meteringCfg.resourceManagerConfig())

		triggerService, err := trigger.NewTriggerService(s.Logger, nil, s.LimitsFactory, meters)
		if err != nil {
			s.Logger.Fatalw("Failed to create cron trigger service", "error", err)
		}
		triggerService.Deployment = meteringCfg.deployment

		return server.NewCronServer(triggerService)
	}, loop.WithOtelViews(trigger.MetricViews()))
}
