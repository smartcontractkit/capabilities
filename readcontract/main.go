package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jonboulle/clockwork"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	actions "github.com/smartcontractkit/capabilities/readcontract/action"
)

var _ loop.StandardCapabilities = (*ReadContractGRPCService)(nil)

const (
	serviceName = "ReadContractCapability"
)

type readContractAction interface {
	capabilities.ExecutableCapability
	Start(context.Context) error
	Close() error
}

type ReadContractGRPCService struct {
	services.StateMachine
	action             readContractAction
	lggr               logger.Logger
	capabilityRegistry core.CapabilitiesRegistry
}

func main() {
	loopserver.Serve(serviceName, func(lggr logger.Logger) *ReadContractGRPCService {
		return &ReadContractGRPCService{lggr: lggr}
	})
}

type readContractRelayer struct {
	relayer core.Relayer
}

func (r *readContractRelayer) NewContractReader(ctx context.Context, contractReaderConfig []byte) (actions.ContractReader, error) {
	return r.relayer.NewContractReader(ctx, contractReaderConfig)
}

func (cs *ReadContractGRPCService) Start(ctx context.Context) error {
	return nil
}

func (cs *ReadContractGRPCService) Close() error {
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

func (cs *ReadContractGRPCService) Ready() error {
	return nil
}

func (cs *ReadContractGRPCService) HealthReport() map[string]error {
	return map[string]error{cs.Name(): nil}
}

func (cs *ReadContractGRPCService) Name() string {
	return cs.lggr.Name()
}

func (cs *ReadContractGRPCService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	triggerInfo, err := cs.action.Info(ctx)
	if err != nil {
		return nil, err
	}

	return []capabilities.CapabilityInfo{
		triggerInfo,
	}, nil
}

func (cs *ReadContractGRPCService) Initialise(
	ctx context.Context,
	config string,
	_ core.TelemetryService,
	_ core.KeyValueStore,
	capabilityRegistry core.CapabilitiesRegistry,
	_ core.ErrorLog,
	_ core.PipelineRunnerService,
	relayerSet core.RelayerSet,
	oracleFactory core.OracleFactory,
	_ core.GatewayConnector,
	_ core.Keystore,
) error {
	cs.lggr.Infof("Initialising %s", serviceName)

	var readContractConfig actions.ReadContractConfig
	err := json.Unmarshal([]byte(config), &readContractConfig)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	relayID := types.NewRelayID(readContractConfig.Network, fmt.Sprintf("%d", readContractConfig.ChainID))
	relayer, err := relayerSet.Get(ctx, relayID)
	if err != nil {
		return fmt.Errorf("failed to fetch relayer for chainID %d from relayerSet: %w", readContractConfig.ChainID, err)
	}

	cs.action, err = actions.NewReadContractAction(ctx, cs.lggr, readContractConfig, &readContractRelayer{relayer},
		oracleFactory, clockwork.NewRealClock())
	if err != nil {
		return fmt.Errorf("failed to create read contract action: %w", err)
	}

	if err = cs.action.Start(ctx); err != nil {
		return fmt.Errorf("failed to start read contract action: %w", err)
	}

	if err := capabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add read contract capability to the capability registry: %w", err)
	}

	cs.capabilityRegistry = capabilityRegistry

	return nil
}
