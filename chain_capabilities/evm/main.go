package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/trigger"

	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"

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

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	capability
	lggr logger.Logger
}

type capability struct {
	actions.EVM
	triggerService *trigger.LogTriggerService
}

var _ evmcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.Serve(CapabilityName, func(lggr logger.Logger) loop.StandardCapabilities {
		return evmcapserver.NewClientServer(&capabilityGRPCService{lggr: lggr})
	})
}

func (c *capabilityGRPCService) Initialise(ctx context.Context, configStr string, _ core.TelemetryService, _ core.KeyValueStore, _ core.ErrorLog, _ core.PipelineRunnerService, relayerSet core.RelayerSet, _ core.OracleFactory, _ core.GatewayConnector) error {
	c.lggr.Infof("Initialising %s", CapabilityName)

	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return fmt.Errorf("failed to parse EVM capability config: %w", err)
	}
	if cfg.LogTriggerPollInterval < 0 {
		return fmt.Errorf("LogTriggerPollInterval must be positive, got: %s", cfg.LogTriggerPollInterval)
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

	if len(common.Hex2Bytes(cfg.CREForwarderAddress)) != 20 {
		return fmt.Errorf("invalid cre forward address, it does not have 20 characters: %s", cfg.CREForwarderAddress)
	}

	if cfg.ReceiverGasMinimum == 0 {
		return fmt.Errorf("invalid ReceiverGasMinimum value. It must be greater than 0. Provided ReceiverGasMinimum %d", cfg.ReceiverGasMinimum)
	}

	evm, err := actions.NewEVM(cfg, evmRelayer, c.lggr)
	if err != nil {
		return fmt.Errorf("failed to init evm relayer for chainID %d from relayer: %w", cfg.ChainID, err)
	}

	c.capability = capability{
		EVM:            evm,
		triggerService: trigger.NewLogTriggerService(evmRelayer, trigger.NewLogTriggerStore(), c.lggr, cfg.LogTriggerPollInterval),
	}

	c.lggr.Infof("Successfully initialised %s", CapabilityName)

	return nil
}

func (c *capabilityGRPCService) Start(_ context.Context) error {
	c.lggr.Infof("Start %s", CapabilityName)
	// TODO PLEX-1456: implement the clean up call here
	return nil
}

func (c *capabilityGRPCService) Close() error {
	c.lggr.Infof("Closing %s", CapabilityName)
	err := c.triggerService.Close()
	if err != nil {
		return err
	}
	// TODO PLEX-1456: also implement the clean up to free up resources in the LogPoller (unregister all filters)
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
	// TODO implement me
	panic("implement me")
}

func (c *capabilityGRPCService) UnregisterFromWorkflow(_ context.Context, _ capabilities.UnregisterFromWorkflowRequest) error {
	// TODO implement me
	panic("implement me")
}

func (c *capabilityGRPCService) RegisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmservice.Log], error) {
	return c.triggerService.RegisterLogTrigger(ctx, triggerID, metadata, input)
}

func (c *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) error {
	return c.triggerService.UnregisterLogTrigger(ctx, triggerID, metadata, input)
}
