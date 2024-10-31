package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/libs/loopserver"
	actions "github.com/smartcontractkit/capabilities/readcontract/action"
)

var _ loop.StandardCapabilities = (*ReadContractGRPCService)(nil)

const (
	serviceName = "ReadContractCapability"
)

type ReadContractGRPCService struct {
	action capabilities.ActionCapability
	s      *loop.Server
}

func main() {
	loopserver.Serve(serviceName, func(s *loop.Server, _ string) *ReadContractGRPCService {
		return &ReadContractGRPCService{s: s}
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
	return nil
}

func (cs *ReadContractGRPCService) Ready() error {
	return nil
}

func (cs *ReadContractGRPCService) HealthReport() map[string]error {
	return nil
}

func (cs *ReadContractGRPCService) Name() string {
	return serviceName
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
	_ core.OracleFactory,
) error {
	cs.s.Logger.Infof("Initialising %s", serviceName)

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

	cs.action, err = actions.NewReadContractAction(cs.s.Logger, readContractConfig, &readContractRelayer{relayer})
	if err != nil {
		return fmt.Errorf("failed to create read contract action: %w", err)
	}

	if err := capabilityRegistry.Add(ctx, cs.action); err != nil {
		return fmt.Errorf("failed to add read contract capability to the capability registry: %w", err)
	}

	return nil
}
