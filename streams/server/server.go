package server

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/streams/trigger"
)

var _ loop.StandardCapabilities = (*capabilitiesServer)(nil)

type capabilitiesServer struct {
	Trigger trigger.CapabilityService
	lggr    logger.Logger
	srvcs   []services.Service
}

func New(lggr logger.Logger) *capabilitiesServer {
	return &capabilitiesServer{lggr: lggr}
}

func (cs *capabilitiesServer) Start(ctx context.Context) error {
	return nil
}

func (cs *capabilitiesServer) Close() (err error) {
	for _, service := range cs.srvcs {
		cs.lggr.Debugw("Closing service...", "name", service.Name())
		err = errors.Join(err, service.Close())
	}
	return err
}

func (cs *capabilitiesServer) Ready() error {
	return nil
}

func (cs *capabilitiesServer) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *capabilitiesServer) Name() string {
	return cs.lggr.Name()
}

func (cs *capabilitiesServer) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	triggerInfo, err := cs.Trigger.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		triggerInfo,
	}, nil
}

func (cs *capabilitiesServer) Initialise(
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
	cs.lggr.Debugf("Initialising")
	t, err := trigger.New(trigger.Params{
		Logger: cs.lggr,
	})
	if err != nil {
		return fmt.Errorf("error when creating trigger: %w", err)
	}
	cs.Trigger = t

	if err := cs.Trigger.Start(ctx); err != nil {
		return fmt.Errorf("error when starting trigger: %w", err)
	}
	cs.srvcs = append(cs.srvcs, cs.Trigger)

	if err := capabilityRegistry.Add(ctx, cs.Trigger); err != nil {
		return fmt.Errorf("error when adding streams trigger to the registry: %w", err)
	}
	return nil
}
