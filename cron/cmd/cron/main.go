package main

import (
	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/capability"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/cron/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

func main() {
	capability.Run(
		"cron",
		newCron,
		capability.WithOtelViews(trigger.MetricViews()...),
		capability.WithConfig[trigger.Config](),
	)
}

func newCron(i do.Injector) (*server.CronServer, error) {
	triggerService, err := trigger.NewTriggerService(
		do.MustInvoke[logger.Logger](i),
		do.MustInvoke[clockwork.Clock](i),
		do.MustInvoke[limits.Factory](i),
		*do.MustInvoke[*trigger.Config](i),
	)
	if err != nil {
		return nil, err
	}
	return server.NewCronServer(triggerService), nil
}
