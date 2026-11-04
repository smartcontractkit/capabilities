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
	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return nil, err
	}

	clock, err := do.Invoke[clockwork.Clock](i)
	if err != nil {
		return nil, err
	}

	factory, err := do.Invoke[limits.Factory](i)
	if err != nil {
		return nil, err
	}

	cfg, err := do.Invoke[*trigger.Config](i)
	if err != nil {
		return nil, err
	}

	triggerService, err := trigger.NewService(
		lggr,
		clock,
		factory,
		*cfg,
	)
	if err != nil {
		return nil, err
	}
	return server.NewCronServer(triggerService), nil
}
