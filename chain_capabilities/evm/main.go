package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/ethereum/go-ethereum/common"
	chainselectors "github.com/smartcontractkit/chain-selectors"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"

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

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const CapabilityName = "evm"

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	chainSelector uint64
	capability
	lggr          logger.Logger
	limitsFactory limits.Factory
}

type capability struct {
	*actions.EVM
	id               string
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

	metrics, err := monitoring.NewMetrics()
	if err != nil {
		return fmt.Errorf("failed to create metrics: %w", err)
	}

	processor, err := monitoring.NewProcessor(c.lggr, metrics)
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
	c.id = "evm" + ":ChainSelector:" + strconv.FormatUint(cs, 10) + "@1.0.0"

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

	var scheduler actions.TransmissionScheduler
	if cfg.DeltaStage > 0 {
		scheduler, err = c.initialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage, cfg.IsLocaL)
		if err != nil {
			return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
		}
	} else {
		c.lggr.Infow("DeltaStage not configured, transmission scheduling disabled")
	}

	c.EVM, err = actions.NewEVM(*cfg, evmRelayer, c.lggr, processor, messageBuilder, c.consensusHandler, c.chainSelector, c.limitsFactory, scheduler)
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

func (c *capabilityGRPCService) initMyDON(ctx context.Context, registry core.CapabilitiesRegistry) error {
	localNode, err := registry.LocalNode(ctx)
	if err != nil {
		return fmt.Errorf("failed to receiver local node: %w", err)
	}

	var dons []capabilities.DON

	donsWithNodes, err := registry.DONsForCapability(ctx, c.id)
	if err != nil {
		return fmt.Errorf("failed getting dons for capability: %w", err)
	}

	for _, d := range donsWithNodes {
		for _, n := range d.Nodes {
			if n.PeerID.String() == localNode.PeerID.String() {
				dons = append(dons, d.DON)
			}
		}
	}

	if len(dons) == 0 {
		return errors.New("failed to find don for my peer ID: " + localNode.PeerID.String())
	}

	if len(dons) > 1 {
		for _, d := range dons {
			c.lggr.Errorf("received more than one don for capability id: %s don id: %d don name: %s", c.id, d.ID, d.Name)
		}
	}

	c.DON = &dons[0]

	return nil
}

func (c *capabilityGRPCService) initialiseTransmissionScheduler(
	ctx context.Context,
	capRegistry core.CapabilitiesRegistry,
	deltaStage time.Duration,
	isLocal bool,
) (actions.TransmissionScheduler, error) {
	if isLocal {
		return actions.TransmissionScheduler{}, nil
	}

	err := c.initMyDON(ctx, capRegistry)
	if err != nil {
		return actions.TransmissionScheduler{}, fmt.Errorf("failed to initialize capability with my don info: %w", err)
	}

	localNode, err := capRegistry.LocalNode(ctx)
	if err != nil {
		return actions.TransmissionScheduler{}, fmt.Errorf("failed to get local node: %w", err)
	}

	if c.DON == nil {
		return actions.TransmissionScheduler{}, errors.New("capabilityInfo DON is nil")
	}

	if len(c.DON.Members) == 0 {
		return actions.TransmissionScheduler{}, errors.New("capabilityInfo DON is empty")
	}

	var donPeerIDs []p2ptypes.PeerID
	myPeerID := localNode.PeerID
	donPeerIDs = append(donPeerIDs, c.DON.Members...)

	if myPeerID == nil {
		return actions.TransmissionScheduler{}, fmt.Errorf("local node peer ID is nil")
	}
	if len(donPeerIDs) == 0 {
		return actions.TransmissionScheduler{}, fmt.Errorf("DON members list is empty")
	}

	found := slices.Contains(donPeerIDs, *myPeerID)
	if !found {
		return actions.TransmissionScheduler{}, fmt.Errorf("local peer ID %s not found in DON members", myPeerID.String())
	}

	c.lggr.Infow("Transmission scheduler initialized",
		"deltaStage", deltaStage,
		"donSize", len(donPeerIDs),
		"F", c.DON.F,
		"myPeerID", myPeerID.String(),
	)

	return actions.NewTransmissionScheduler(
		*myPeerID,
		donPeerIDs,
		deltaStage,
		c.DON.F,
		c.lggr,
	), nil
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

	// DeltaStage is optional - if not set, transmission scheduling will be disabled
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
