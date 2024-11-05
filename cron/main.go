package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

var _ loop.StandardCapabilities = (*CapabilitiesService)(nil)

const (
	serviceName = "CronCapabilities"
)

type TriggerCapabilityService interface {
	services.Service
	capabilities.TriggerCapability
}

type CapabilitiesService struct {
	trigger TriggerCapabilityService
	s       *loop.Server
	srvcs   []services.Service
}

func main() {
	loopserver.Serve(serviceName, func(s *loop.Server, _ string) *CapabilitiesService {
		return &CapabilitiesService{s: s}
	})
}

func (cs *CapabilitiesService) Start(ctx context.Context) error {
	return nil
}

func (cs *CapabilitiesService) Close() (err error) {
	for _, service := range cs.srvcs {
		cs.s.Logger.Debugw("Closing service...", "name", service.Name())
		err = errors.Join(err, service.Close())
	}

	return err
}

func (cs *CapabilitiesService) Ready() error {
	return nil
}

func (cs *CapabilitiesService) HealthReport() map[string]error {
	return nil
}

func (cs *CapabilitiesService) Name() string {
	return serviceName
}

func (cs *CapabilitiesService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	triggerInfo, err := cs.trigger.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		triggerInfo,
	}, nil
}

func (cs *CapabilitiesService) Initialise(
	ctx context.Context,
	_ string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	_ core.RelayerSet,
	_ core.OracleFactory,
) error {
	cs.s.Logger.Debugf("Initialising %s", serviceName)
	cs.trigger = trigger.New(trigger.Params{
		Logger: cs.s.Logger,
	})
	if err := cs.trigger.Start(ctx); err != nil {
		return fmt.Errorf("error when starting trigger: %w", err)
	}
	cs.srvcs = append(cs.srvcs, cs.trigger)

	if err := capabilityRegistry.Add(ctx, cs.trigger); err != nil {
		return fmt.Errorf("error when adding trigger to the registry: %w", err)
	}

	return nil
}
