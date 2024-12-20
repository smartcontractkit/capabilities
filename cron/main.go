package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
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
	trigger            TriggerCapabilityService
	lggr               logger.Logger
	srvcs              []services.Service
	capabilityRegistry core.CapabilitiesRegistry
}

func main() {
	loopserver.Serve(serviceName, func(lggr logger.Logger) *CapabilitiesService {
		return &CapabilitiesService{lggr: lggr}
	})
}

func (cs *CapabilitiesService) Start(ctx context.Context) error {
	return nil
}

func (cs *CapabilitiesService) Close() (err error) {
	for _, service := range cs.srvcs {
		cs.lggr.Debugw("Closing service...", "name", service.Name())
		err = errors.Join(err, service.Close())
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = errors.Join(err, cs.capabilityRegistry.Remove(ctx, trigger.ID))
	return err
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
	config string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	_ core.RelayerSet,
	_ core.OracleFactory,
) error {
	cs.lggr.Debugf("Initialising %s", serviceName)

	var cronConfig trigger.Config
	if len(config) > 0 {
		err := json.Unmarshal([]byte(config), &cronConfig)
		if err != nil {
			return fmt.Errorf("failed to unmarshal config: %s %w", config, err)
		}
	}

	cs.trigger = trigger.New(trigger.Params{
		Logger: cs.lggr,
		Config: cronConfig,
	})
	if err := cs.trigger.Start(ctx); err != nil {
		return fmt.Errorf("error when starting trigger: %w", err)
	}
	cs.srvcs = append(cs.srvcs, cs.trigger)

	cs.capabilityRegistry = capabilityRegistry
	if err := capabilityRegistry.Add(ctx, cs.trigger); err != nil {
		return fmt.Errorf("error when adding trigger to the registry: %w", err)
	}

	return nil
}
