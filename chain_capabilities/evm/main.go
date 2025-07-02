package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/oracle"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/poller"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/trigger"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"

	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	CapabilityName = "evm"
	// OCRRoundBatchSize - max number of requests that this node will try to process in a single round
	// TODO PLEX-1569: make configurable
	OCRRoundBatchSize = 50
	// OCRRoundMaxBatchSize - defines max number of requests that this node will process in a round, if requested by another node.
	// Needed to allow graceful roll out of OCRBatchSize increase.
	OCRRoundMaxBatchSize = 500
	PollingWorkersNum    = 10
	PollPeriod           = 10 * time.Second
	repoCLLCapabilities  = "https://raw.githubusercontent.com/smartcontractkit/capabilities"
	versionRefsMain      = "refs/heads/main"
	schemaBasePath       = repoCLLCapabilities + "/" + versionRefsMain + "/chain_capabilities/evm/monitoring"
)

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	capability
	lggr logger.Logger
}

type capability struct {
	actions.EVM
	requestPoller    *poller.Poller
	consensusHandler *consensus.Handler
	oracle           core.Oracle
	triggerService   *trigger.LogTriggerService
}

var _ evmcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.Serve(CapabilityName, func(lggr logger.Logger) loop.StandardCapabilities {
		return evmcapserver.NewClientServer(&capabilityGRPCService{lggr: lggr})
	})
}

func (c *capabilityGRPCService) Initialise(ctx context.Context, configStr string, _ core.TelemetryService, _ core.KeyValueStore, _ core.ErrorLog, _ core.PipelineRunnerService, relayerSet core.RelayerSet, oracleFactory core.OracleFactory, _ core.GatewayConnector) error {
	c.lggr.Infof("Initialising %s", CapabilityName)

	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return fmt.Errorf("failed to parse EVM capability config: %w", err)
	}

	if cfg.LogTriggerPollInterval < 0 {
		return fmt.Errorf("logTriggerPollInterval must be positive, got: %s", cfg.LogTriggerPollInterval)
	}

	client := beholder.GetClient().ForName("evm_capability")
	metrics, err := monitoring.NewMetrics()
	if err != nil {
		return fmt.Errorf("failed to create metrics: %w", err)
	}
	processor, err := monitoring.NewProcessor(beholder.NewProtoEmitter(c.lggr, &client, schemaBasePath), metrics)
	if err != nil {
		return fmt.Errorf("failed to create monitoring proto processor: %w", err)
	}

	relayID := types.NewRelayID(cfg.Network, fmt.Sprintf("%d", cfg.ChainID))
	relayer, err := relayerSet.Get(ctx, relayID)
	if err != nil {
		return fmt.Errorf("failed to fetch relayer for chainID %d from relayerSet: %w", cfg.ChainID, err)
	}

	chainInfo, err := relayer.GetChainInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch chain info for chainID %d from relayer: %w", cfg.ChainID, err)
	}

	messageBuilder := monitoring.NewMessageBuilder(chainInfo, c.CapabilityInfo, cfg.NodeAddress)

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

	c.requestPoller = poller.NewPoller(c.lggr, PollingWorkersNum, PollPeriod)
	c.consensusHandler = consensus.NewHandler(c.lggr, c.requestPoller, time.Second*10)

	c.EVM, err = actions.NewEVM(cfg, evmRelayer, c.lggr, processor, messageBuilder, c.consensusHandler)
	if err != nil {
		return fmt.Errorf("failed to init evm relayer for chainID %d from relayer: %w", cfg.ChainID, err)
	}

	c.triggerService = trigger.NewLogTriggerService(evmRelayer, trigger.NewLogTriggerStore(), c.lggr, cfg.LogTriggerPollInterval)

	// TODO PLEX-1560: populate with implementation
	blocksProvider := &oracle.NullBlocksProvider{}

	c.oracle, err = oracleFactory.NewOracle(ctx, core.OracleArgs{
		LocalConfig: ocrtypes.LocalConfig{
			BlockchainTimeout:                  time.Second * 20,
			ContractConfigTrackerPollInterval:  time.Second * 10,
			ContractConfigConfirmations:        1,
			ContractTransmitterTransmitTimeout: time.Second * 10,
			DatabaseTimeout:                    time.Second * 10,
		},
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(logger.Sugared(c.lggr), c.consensusHandler, blocksProvider, OCRRoundBatchSize, OCRRoundMaxBatchSize),
		ContractTransmitter:           oracle.NewContractTransmitter(c.lggr, c.consensusHandler),
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}

	services := []interface{ Start(context.Context) error }{c.consensusHandler, c.requestPoller, c.oracle}
	for _, service := range services {
		if err := service.Start(ctx); err != nil {
			return err
		}
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
	return errors.Join(c.requestPoller.Close(), c.consensusHandler.Close(), c.oracle.Close(context.Background()), c.triggerService.Close())
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

func (c *capabilityGRPCService) RegisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmcappb.Log], error) {
	return c.triggerService.RegisterLogTrigger(ctx, triggerID, metadata, input)
}

func (c *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) error {
	return c.triggerService.UnregisterLogTrigger(ctx, triggerID, metadata, input)
}
