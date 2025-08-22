package main

import (
	"context"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	action "github.com/smartcontractkit/capabilities/decrypter/action"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

var _ loop.StandardCapabilities = (*capabilitiesServer)(nil)

type capabilitiesServer struct {
	action             capabilities.ExecutableCapability
	lggr               logger.SugaredLogger
	capabilityRegistry core.CapabilitiesRegistry
}

func New(lggr logger.Logger) *capabilitiesServer {
	return &capabilitiesServer{lggr: logger.Sugared(lggr)}
}

func (c *capabilitiesServer) Initialise(
	ctx context.Context,
	_ string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	_ core.RelayerSet,
	oracleFactory core.OracleFactory,
	_ core.GatewayConnector,
	keystore core.Keystore,
) error {
	c.lggr.Debug("Initialising")

	var err error
	c.action, err = action.New(action.Params{
		Logger:   c.lggr,
		Keystore: keystore,
	})
	if err != nil {
		return err
	}

	if err := capabilityRegistry.Add(ctx, c.action); err != nil {
		return fmt.Errorf("failed to add decrypter capability to the capability registry: %w", err)
	}

	c.capabilityRegistry = capabilityRegistry

	return nil
}

func (c *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (c *capabilitiesServer) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := c.capabilityRegistry.Remove(ctx, "")
	if err != nil {
		return err
	}

	return nil
}

func (c *capabilitiesServer) Ready() error {
	return nil
}

func (c *capabilitiesServer) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *capabilitiesServer) Name() string {
	return c.lggr.Name()
}

func (c *capabilitiesServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	actionInfo, err := c.action.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		actionInfo,
	}, nil
}

func main() {
	loopserver.Serve("Decrypter", New)
}
