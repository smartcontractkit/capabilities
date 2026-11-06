package main

//go:generate go run . docgen

import (
	"github.com/smartcontractkit/capabilities/internal/consensus/metrics"
	"github.com/smartcontractkit/capabilities/internal/consensus/service"
	"github.com/smartcontractkit/capabilities/libs/capability"
)

func main() {
	capability.Run("consensus", service.New, capability.WithOtelViews(metrics.MetricViews()...))
}
