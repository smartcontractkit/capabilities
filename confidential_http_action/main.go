package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	action "github.com/smartcontractkit/capabilities/confidential_http_action/action"
	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
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

func (cs *CapabilitiesService) Start(ctx context.Context) error {
	return nil
}

func (cs *CapabilitiesService) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	info, err := cs.action.Info(ctx)
	if err != nil {
		return err
	}
	err = cs.capabilityRegistry.Remove(ctx, info.ID)
	if err != nil {
		return err
	}
	return nil
}

func (cs *CapabilitiesService) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *CapabilitiesService) Name() string {
	return serviceName
}

func (cs *CapabilitiesService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	triggerInfo, err := cs.action.Info(ctx)
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
	relayerSet core.RelayerSet,
	oracleFactory core.OracleFactory,
	_ core.GatewayConnector) error {
	cs.lggr.Infof("Initialising %s", serviceName)
	cs.lggr.Infof("Config: %s", config)
	var capConfig cap.Config
	err := json.Unmarshal([]byte(config), &capConfig)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	cs.action, err = action.New(cs.lggr, capConfig)
	if err != nil {
		return fmt.Errorf("failed to create attested http action: %w", err)
	}

	if err = cs.action.Start(ctx); err != nil {
		return fmt.Errorf("failed to start attested http action: %w", err)
	}

	if err := capabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add attested http capability to the capability registry: %w", err)
	}

	cs.capabilityRegistry = capabilityRegistry

	return nil
}
