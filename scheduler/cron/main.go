package main

//go:generate go run . docgen

import (
	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/scheduler/cron/service"
)

func main() {
	capability.Run("cron", service.New, capability.WithOtelViews(service.MetricViews()...))
}
