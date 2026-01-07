package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	chainselectors "github.com/smartcontractkit/chain-selectors"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/height"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"

	consMetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/poller"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	CapabilityName = "evm"

	repoCLLCapabilities = "https://raw.githubusercontent.com/smartcontractkit/capabilities"
	versionRefsMain     = "refs/heads/main"
	schemaBasePath      = repoCLLCapabilities + "/" + versionRefsMain + "/chain_capabilities/evm/monitoring"
)

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	chainSelector uint64
	capability
	lggr          logger.Logger
	limitsFactory limits.Factory
}

type capability struct {
	*actions.EVM
	requestPoller    *poller.Poller
	consensusHandler *chainconsensus.Handler
	oracle           core.Oracle
	triggerService   *trigger.LogTriggerService
	heightProvider   *height.Provider
}

var _ evmcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return evmcapserver.NewClientServer(&capabilityGRPCService{lggr: s.Logger, limitsFactory: s.LimitsFactory})
	}, loop.WithOtelViews(consMetrics.MetricViews()))
}

func (c *capabilityGRPCService) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	c.lggr.Infof("Initialising %s", CapabilityName)

	cfg, err := c.unmarshalConfig(dependencies.Config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	c.lggr.Infof("Initialising %s, ChainId: %d, Network: %s", CapabilityName, cfg.ChainID, cfg.Network)

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
	relayer, err := dependencies.RelayerSet.Get(ctx, relayID)
	if err != nil {
		return fmt.Errorf("failed to fetch relayer for chainID %d from relayerSet: %w", cfg.ChainID, err)
	}

	cs, ok := chainselectors.EvmChainIdToChainSelector()[cfg.ChainID]
	if !ok {
		return fmt.Errorf("chain selector not found for chainID: %d", cfg.ChainID)
	}

	c.chainSelector = cs

	chainInfo, err := relayer.GetChainInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch chain info for chainID %d from relayer: %w", cfg.ChainID, err)
	}

	messageBuilder := monitoring.NewMessageBuilder(chainInfo, c.CapabilityInfo, cfg.NodeAddress)

	evmRelayer, err := relayer.EVM()
	if err != nil {
		return fmt.Errorf("failed to init evm relayer for chainID %d from relayer: %w", cfg.ChainID, err)
	}

	consensusMetrics, err := consMetrics.NewConsensusMetrics(chainInfo)
	if err != nil {
		return fmt.Errorf("failed to create evm consensus metrics: %w", err)
	}
	c.requestPoller = poller.NewPoller(c.lggr, consensusMetrics, cfg.ObservationPollerWorkersCount, cfg.ObservationPollPeriod)
	c.consensusHandler = chainconsensus.NewHandler(c.lggr, c.requestPoller, consensusMetrics, cfg.UnknownRequestsTTL)

	c.EVM, err = actions.NewEVM(*cfg, evmRelayer, c.lggr, processor, messageBuilder, c.consensusHandler, c.chainSelector, c.limitsFactory)
	if err != nil {
		return fmt.Errorf("failed to init evm relayer for chainID %d from relayer: %w", cfg.ChainID, err)
	}

	// TODO: add org resolver
	c.triggerService, err = trigger.NewLogTriggerService(evmRelayer, trigger.NewLogTriggerStore(), c.lggr, processor, messageBuilder,
		cfg.LogTriggerPollInterval, cfg.LogTriggerSendChannelBufferSize, cfg.LogTriggerLimitQueryLogSize, c.limitsFactory, dependencies.OrgResolver)
	if err != nil {
		return fmt.Errorf("error when creating trigger: %w", err)
	}

	c.heightProvider = height.NewProvider(c.lggr, cfg.ChainHeightPollPeriod, evmRelayer)

	c.oracle, err = dependencies.OracleFactory.NewOracle(ctx, core.OracleArgs{
		LocalConfig: ocrtypes.LocalConfig{
			BlockchainTimeout:                  time.Second * 20,
			ContractConfigTrackerPollInterval:  time.Second * 10,
			ContractConfigConfirmations:        1,
			ContractTransmitterTransmitTimeout: time.Second * 10,
			DatabaseTimeout:                    time.Second * 10,
			ContractConfigLoadTimeout:          time.Second * 10,
			DefaultMaxDurationInitialization:   time.Second * 10,
		},
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(logger.Sugared(c.lggr), c.consensusHandler, c.heightProvider, consensusMetrics),
		ContractTransmitter:           oracle.NewContractTransmitter(c.lggr, c.consensusHandler),
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}

	startServices := []interface{ Start(context.Context) error }{c.consensusHandler, c.requestPoller, c.oracle, c.heightProvider, c.triggerService}
	for _, service := range startServices {
		if err := service.Start(ctx); err != nil {
			return err
		}
	}

	c.lggr.Infof("Successfully initialised %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) unmarshalConfig(configStr string) (*config.Config, error) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse EVM capability config: %w", err)
	}

	if cfg.LogTriggerPollInterval < 0 {
		return nil, fmt.Errorf("logTriggerPollInterval must be positive, got: %s", cfg.LogTriggerPollInterval)
	}

	if !common.IsHexAddress(cfg.CREForwarderAddress) {
		return nil, fmt.Errorf("invalid cre forward address, it does not have 20 characters: %s", cfg.CREForwarderAddress)
	}

	if cfg.ReceiverGasMinimum == 0 {
		return nil, fmt.Errorf("invalid ReceiverGasMinimum value. It must be greater than 0. Provided ReceiverGasMinimum %d", cfg.ReceiverGasMinimum)
	}

	if cfg.ObservationPollerWorkersCount == 0 {
		cfg.ObservationPollerWorkersCount = 10
		c.lggr.Infof("ObservationPollerWorkersCount is zero, setting to %d.", cfg.ObservationPollerWorkersCount)
	}

	if cfg.ObservationPollPeriod == 0 {
		cfg.ObservationPollPeriod = 2 * time.Second
		c.lggr.Infof("ObservationPollPeriod is zero, setting to %s.", cfg.ObservationPollPeriod)
	}

	if cfg.ChainHeightPollPeriod == 0 {
		cfg.ChainHeightPollPeriod = time.Second
		c.lggr.Infof("ChainHeightPollPeriod is zero, setting to %s.", cfg.ChainHeightPollPeriod)
	}

	if cfg.UnknownRequestsTTL == 0 {
		cfg.UnknownRequestsTTL = 10 * time.Second
		c.lggr.Infof("UnknownRequestsTTL is zero, setting to %s.", cfg.UnknownRequestsTTL)
	}

	return &cfg, nil
}

func (c *capabilityGRPCService) Start(_ context.Context) error {
	c.lggr.Infof("Start %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) Close() error {
	c.lggr.Infof("Closing %s", CapabilityName)
	return errors.Join(c.EVM.Close(), c.requestPoller.Close(), c.consensusHandler.Close(), c.oracle.Close(context.Background()), c.triggerService.Close(), c.heightProvider.Close())
}

func (c *capabilityGRPCService) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *capabilityGRPCService) Name() string {
	return c.lggr.Name()
}

func (c *capabilityGRPCService) ChainSelector() uint64 {
	return c.chainSelector
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

func (c *capabilityGRPCService) RegisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*evmcappb.Log], caperrors.Error) {
	return c.triggerService.RegisterLogTrigger(ctx, triggerID, metadata, input)
}

func (c *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *evmcappb.FilterLogTriggerRequest) caperrors.Error {
	return c.triggerService.UnregisterLogTrigger(ctx, triggerID, metadata, input)
}
