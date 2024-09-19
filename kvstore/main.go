package main

import (
	"context"
	"fmt"

	"github.com/hashicorp/go-plugin"

	"github.com/smartcontractkit/capabilities/kvstore/target"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	serviceName = "KVStoreCapabilities"
)

type CapabilitiesService struct {
	target capabilities.TargetCapability
	s      *loop.Server
}

func main() {
	s := loop.MustNewStartedServer(serviceName)
	defer s.Stop()

	s.Logger.Infof("Starting service %s", serviceName)

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
	targetInfo, err := cs.target.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		targetInfo,
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
	cs.target = target.New(target.Params{
		Store:  store,
		Logger: cs.s.Logger,
	})

	if err := capabilityRegistry.Add(ctx, cs.target); err != nil {
		return fmt.Errorf("error when adding kv store target to the registry: %w", err)
	}

	return nil
}
