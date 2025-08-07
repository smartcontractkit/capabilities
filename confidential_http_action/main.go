package main

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	action "github.com/smartcontractkit/capabilities/confidential_http_action/action"
)

const (
	serviceName = "ConfidentialHTTPCapability"
)

var _ loop.StandardCapabilities = (*capabilitiesServer)(nil)

type capabilitiesServer struct {
	action capabilities.ExecutableCapability
	lggr   logger.Logger
}

func New(lggr logger.Logger) *capabilitiesServer {
	return &capabilitiesServer{lggr: logger.Sugared(lggr)}
}

func main() {
	loopserver.Serve(serviceName, New)
}
func (cs *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (cs *capabilitiesServer) Close() error {
	return nil
}

func (cs *capabilitiesServer) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *capabilitiesServer) Name() string {
	return serviceName
}

func (cs *capabilitiesServer) Ready() error {
	return nil
}

func (cs *capabilitiesServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	if cs.action == nil {
		return nil, fmt.Errorf("action capability not initialized")
	}
	triggerInfo, err := cs.action.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		triggerInfo,
	}, nil
}

func (cs *capabilitiesServer) Initialise(
	ctx context.Context,
	config string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	_ core.RelayerSet,
	_ core.OracleFactory,
	_ core.GatewayConnector,
	keystore core.Keystore) error {

	cs.lggr.Infof("Initialising %s", serviceName)
	cs.lggr.Infof("Config: %s", config)

	// All dependencies needed to initialize the action are passed here.
	// The action itself will handle the lazy initialization.
	cs.action = action.New(cs.lggr, config, keystore, capabilityRegistry)
	if err := capabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add attested http capability to the capability registry: %w", err)
	}
	return nil
}
