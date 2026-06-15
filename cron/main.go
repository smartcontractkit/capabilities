package main

import (
	"os"
	"strconv"

	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/resourcemanager"
)

// meterRecordsEnabledEnvVar gates MeterRecord emission; the name is the
// cross-producer convention for the metering rollout (SHARED-2718).
const meterRecordsEnabledEnvVar = "CL_METER_RECORDS_ENABLED"

// meterRecordsEnabled reads the metering gate from the environment. Unset or
// unparseable values disable emission; metering config must never prevent the
// capability from starting.
func meterRecordsEnabled(lggr logger.Logger) bool {
	v := os.Getenv(meterRecordsEnabledEnvVar)
	if v == "" {
		return false
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		lggr.Warnw("Invalid value for "+meterRecordsEnabledEnvVar+", meter record emission disabled", "value", v, "error", err)
		return false
	}
	return enabled
}

func main() {
	loopserver.ServeNew(trigger.ServiceName, func(s *loop.Server) loop.StandardCapabilities {
		meters := resourcemanager.NewResourceManager(s.Logger, resourcemanager.ResourceManagerConfig{
			Enabled:          meterRecordsEnabled(s.Logger),
			Emitter:          beholder.GetEmitter(),
			SnapshotInterval: resourcemanager.DefaultSnapshotInterval,
		})

		triggerService, err := trigger.NewTriggerService(s.Logger, nil, s.LimitsFactory, meters)
		if err != nil {
			s.Logger.Fatalw("Failed to create cron trigger service", "error", err)
		}

		return server.NewCronServer(triggerService)
	}, loop.WithOtelViews(trigger.MetricViews()))
}
