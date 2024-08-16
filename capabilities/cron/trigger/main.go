package main

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/go-plugin"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

const (
	loggerName = "PluginStandardCapability"
)

func main() {
	s := loop.MustNewStartedServer(loggerName)
	defer s.Stop()

	stopCh := make(chan struct{})
	defer close(stopCh)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: loop.StandardCapabilitiesHandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			loop.PluginStandardCapabilitiesName: &loop.StandardCapabilitiesLoop{
				PluginServer: &CapabilityService{},
				BrokerConfig: loop.BrokerConfig{Logger: s.Logger, StopCh: stopCh, GRPCOpts: s.GRPCOpts},
			},
		},
		GRPCServer: s.GRPCOpts.NewServer,
	})

}

type Output struct {
	Timestamp string
}

type CapabilityService struct {
	telemetryService core.TelemetryService
	store            core.KeyValueStore
	config           string
}

func (c CapabilityService) Start(ctx context.Context) error {
	return nil
}

func (c CapabilityService) Close() error {
	return nil
}

func (c CapabilityService) Ready() error {
	return nil
}

func (c CapabilityService) HealthReport() map[string]error {
	return nil
}

func (c CapabilityService) Name() string {
	return ""
}

func (c CapabilityService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	return []capabilities.CapabilityInfo{
		{
			ID:             "cron-trigger@1.0.0",
			CapabilityType: capabilities.CapabilityTypeTrigger,
			Description:    "Trigger based on a CRON schedule",
		}}, nil

}

func (c CapabilityService) RegisterTrigger(ctx context.Context, request capabilities.CapabilityRequest) (<-chan capabilities.CapabilityResponse, error) {

	result := make(chan capabilities.CapabilityResponse, 100)

	err := c.store.Store(ctx, "key", []byte("value"))
	if err != nil {
		return nil, fmt.Errorf("failed to store key: %w", err)
	}

	go func() {
		defer close(result)
		for i := 0; i < 2; i++ {
			output := Output{
				Timestamp: fmt.Sprintf(time.Now().Format(time.RFC3339)),
			}
			outputMap, err := values.NewMap(map[string]any{
				"timestamp": output.Timestamp,
			})

			if err != nil {
				return
			}

			result <- capabilities.CapabilityResponse{
				Value: outputMap,
				Err:   nil,
			}
			time.Sleep(1 * time.Second)
		}
	}()

	return result, nil
}

func (c CapabilityService) UnregisterTrigger(ctx context.Context, request capabilities.CapabilityRequest) error {
	return nil
}

func (c CapabilityService) Initialise(ctx context.Context, config string, telemetryService core.TelemetryService, store core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry, errorLog core.ErrorLog,
	pipelineRunner core.PipelineRunnerService, relayerSet core.RelayerSet) error {

	c.telemetryService = telemetryService
	c.store = store
	c.config = config

	capabilitiesInfo, err := c.Infos(ctx)
	if err != nil {
		return fmt.Errorf("failed to get capability infos: %w", err)
	}

	for _, info := range capabilitiesInfo {
		if err := capabilityRegistry.Add(ctx, info); err != nil {
			return fmt.Errorf("error when adding capability info to registry: %w", err)
		}
	}

	return nil
}
