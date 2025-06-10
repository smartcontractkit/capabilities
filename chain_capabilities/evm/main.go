package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chain_capabilities/evm/actions"

	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	CapabilityName = "evm"
)

type Config struct {
	ChainID uint64 `json:"chainId"`
	Network string `json:"network"`
}

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	capability
	lggr logger.Logger
}

type capability struct {
	actions.EVM
}

var _ evmcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.Serve(CapabilityName, func(lggr logger.Logger) loop.StandardCapabilities {
		return evmcapserver.NewClientServer(&capabilityGRPCService{lggr: lggr})
	})
}

func (c *capabilityGRPCService) Initialise(ctx context.Context, config string, _ core.TelemetryService, _ core.KeyValueStore, _ core.ErrorLog, _ core.PipelineRunnerService, relayerSet core.RelayerSet, _ core.OracleFactory) error {
	c.lggr.Infof("Initialising %s", CapabilityName)

	var cfg Config
	if err := json.Unmarshal([]byte(config), &cfg); err != nil {
		return fmt.Errorf("failed to parse EVM capability config: %w", err)
	}

	relayID := types.NewRelayID(cfg.Network, fmt.Sprintf("%d", cfg.ChainID))

	relayer, err := relayerSet.Get(ctx, relayID)
	if err != nil {
		return fmt.Errorf("failed to fetch relayer for chainID %d from relayerSet: %w", cfg.ChainID, err)
	}

	evmRelayer, err := relayer.EVM()
	if err != nil {
		return fmt.Errorf("failed to init evm relayer for chainID %d from relayer: %w", cfg.ChainID, err)
	}

	c.capability = capability{EVM: actions.NewEVM(evmRelayer)}

	c.lggr.Infof("Successfully initialised %s", CapabilityName)

	return nil
}

func (c *capabilityGRPCService) Start(_ context.Context) error {
	return nil
}

func (c *capabilityGRPCService) Close() error {
	return nil
}

func (c *capabilityGRPCService) HealthReport() map[string]error {
	return map[string]error{}
}

func (c *capabilityGRPCService) Name() string {
	return CapabilityName
}

func (c *capabilityGRPCService) Description() string {
	return "Contains EVM chain functionalities"
}

func (c *capabilityGRPCService) Ready() error {
	return nil
}

func (c *capabilityGRPCService) RegisterToWorkflow(_ context.Context, _ capabilities.RegisterToWorkflowRequest) error {
	//TODO implement me
	panic("implement me")
}

func (c *capabilityGRPCService) UnregisterFromWorkflow(_ context.Context, _ capabilities.UnregisterFromWorkflowRequest) error {
	//TODO implement me
	panic("implement me")
}
