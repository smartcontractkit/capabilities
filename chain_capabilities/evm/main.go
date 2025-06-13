package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/chain_capabilities/evm/config"

	"math/big"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm/server"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	CapabilityName = "evm"
)

type logTriggerState struct {
	cancelFunc context.CancelFunc
	lastBlock  *big.Int
}

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	capability
	// PLEX-1525 - Provide support for beholder metrics
	emitter custmsg.MessageEmitter

	lggr logger.Logger

	mutexCapabilityTriggers sync.RWMutex
	triggers                map[string]*logTriggerState // key: triggerID
	logTriggerPollInterval  time.Duration
	blockDepth              int64
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

func (c *capabilityGRPCService) Initialise(ctx context.Context, cfgstr string, _ core.TelemetryService, _ core.KeyValueStore, _ core.ErrorLog, _ core.PipelineRunnerService, relayerSet core.RelayerSet, _ core.OracleFactory) error {
	c.lggr.Infof("Initialising %s", CapabilityName)

	var cfg config.Config
	if err := json.Unmarshal([]byte(cfgstr), &cfg); err != nil {
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

	if len(common.Hex2Bytes(cfg.KeystoneForwarderAddress)) != 20 {
		return fmt.Errorf("invalid keystone forward address, it does not have 20 characters: %s", cfg.KeystoneForwarderAddress)
	}

	if cfg.ReceiverGasMinimum == 0 {
		return fmt.Errorf("invalid ReceiverGasMinimum value. It must be greater than 0. Provided ReceiverGasMinimum %d", cfg.ReceiverGasMinimum)
	}

	evm, err := actions.NewEVM(cfg, evmRelayer, c.lggr)
	if err != nil {
		return err
	}
	c.capability = capability{evm}
	c.logTriggerPollInterval = cfg.LogTriggerPollInterval
	c.blockDepth = cfg.BlockDepth
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
