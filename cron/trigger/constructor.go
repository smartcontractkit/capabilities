package trigger

import (
	"github.com/smartcontractkit/capabilities/cron/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

// NewCron is the constructor a cron binary hands to capability.Run. The parameter list is the
// declaration of what cron needs: the process's logger, its own config (bound by the host under
// the binary's name), and the limits the schedules it accepts are bounded by. Run reads that list
// and builds what it asks for.
//
// The clock is nil - the real one. nil is NewTriggerService's "a test drives this one".
//
// The generated server is what is returned rather than the service: it is the
// capability.Capability - servable, registerable, and carrying the proto service the requests are
// shaped by.
func NewCron(lggr logger.Logger, cfg Config, lf limits.Factory) (*protos.CronServer, error) {
	triggerService, err := NewTriggerService(lggr, nil, cfg, Dependencies{LimitsFactory: lf})
	if err != nil {
		return nil, err
	}
	return protos.NewCronServer(triggerService), nil
}
