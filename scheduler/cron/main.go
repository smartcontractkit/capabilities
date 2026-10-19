package main

//go:generate go run . docgen

import (
	"context"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/scheduler/cron/service"
)

func main() {
	capability.Run(context.Background(), service.New, capability.WithOtelViews(service.MetricViews()...))
}
