package main

import (
	"context"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	serviceName = "ConfidentialHTTPCapability"
)

type confidentialhttpaction interface {
	capabilities.ExecutableCapability
	Start(context.Context) error
	Close() error
}

type CapabilitiesService struct {
	services.StateMachine
	action             confidentialhttpaction
	lggr               logger.Logger
	capabilityRegistry core.CapabilitiesRegistry
}

func main() {
	loopserver.Serve(serviceName, func(lggr logger.Logger) *CapabilitiesService {
		return &CapabilitiesService{lggr: lggr}
	})
}
