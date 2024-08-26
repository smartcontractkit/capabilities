package main

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-plugin"

	"github.com/smartcontractkit/capabilities/cron/trigger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	serviceName = "CronCapabilities"
)

type CapabilitiesService struct {
	trigger capabilities.TriggerCapability
	s       *loop.Server
}

func main() {
	s := loop.MustNewStartedServer(serviceName)
	defer s.Stop()

	s.Logger.Infof("Starting %s", serviceName)

	stopCh := make(chan struct{})
	defer close(stopCh)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: loop.StandardCapabilitiesHandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			loop.PluginStandardCapabilitiesName: &loop.StandardCapabilitiesLoop{
				PluginServer: &CapabilitiesService{
					s: s,
				},
				BrokerConfig: loop.BrokerConfig{Logger: s.Logger, StopCh: stopCh, GRPCOpts: s.GRPCOpts},
			},
		},
		GRPCServer: s.GRPCOpts.NewServer,
	})
}

func (cs *CapabilitiesService) Start(ctx context.Context) error {
	return nil
}

func (cs *CapabilitiesService) Close() error {
	return nil
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
	config string,
	telemetryService core.TelemetryService,
	store core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	errorLog core.ErrorLog,
	pipelineRunner core.PipelineRunnerService,
	relayerSet core.RelayerSet,
) error {
	cs.s.Logger.Debugf("Initialising %s", serviceName)
	cs.trigger = trigger.New(trigger.Params{
		Logger: cs.s.Logger,
		Clock: nil, 
	})

	if err := capabilityRegistry.Add(ctx, cs.trigger); err != nil {
		return fmt.Errorf("error when adding cron trigger to the registry: %w", err)
	}

	return nil
}
