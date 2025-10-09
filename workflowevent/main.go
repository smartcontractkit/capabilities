package main

import (
	"context"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/capabilities/workflowevent/target"
)

const (
	serviceName = "WorkflowEventCapabilities"
)

type CapabilitiesService struct {
	target             capabilities.ExecutableCapability
	lggr               logger.Logger
	capabilityRegistry core.CapabilitiesRegistry
}

func main() {
	loopserver.ServeNew(serviceName, func(s *loop.Server) loop.StandardCapabilities {
		return &CapabilitiesService{lggr: s.Logger}
	})
}

func (cs *CapabilitiesService) Start(ctx context.Context) error {
	return nil
}

func (cs *CapabilitiesService) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := cs.capabilityRegistry.Remove(ctx, target.ID)
	if err != nil {
		return err
	}
	return nil
}

func (cs *CapabilitiesService) Ready() error {
	return nil
}

func (cs *CapabilitiesService) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *CapabilitiesService) Name() string {
	return cs.lggr.Name()
}

func (cs *CapabilitiesService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	targetInfo, err := cs.target.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		targetInfo,
	}, nil
}

func (cs *CapabilitiesService) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	cs.lggr.Debugf("Initialising %s", serviceName)

	target, err := target.New(target.Params{
		Logger: cs.lggr,
	})
	if err != nil {
		return fmt.Errorf("error creating telemetry target: %w", err)
	}

	cs.target = target

	if err := dependencies.CapabilityRegistry.Add(ctx, cs.target); err != nil {
		return fmt.Errorf("error when adding telemetry target to the registry: %w", err)
	}

	cs.capabilityRegistry = dependencies.CapabilityRegistry

	return nil
}
