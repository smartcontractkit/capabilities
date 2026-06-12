package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	chain_selectors "github.com/smartcontractkit/chain-selectors"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	consMetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/poller"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	solcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana/server"
)

const (
	CapabilityName = "solana"

	repoCLLCapabilities = "https://raw.githubusercontent.com/smartcontractkit/capabilities"
	versionRefsMain     = "refs/heads/main"
	schemaBasePath      = repoCLLCapabilities + "/" + versionRefsMain + "/chain_capabilities/solana/monitoring"
)

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	chainSelector uint64
	capability
	lggr           logger.Logger
	limitsFactory  limits.Factory
	triggerService *trigger.SolanaLogTriggerService
}

type capability struct {
	*actions.Solana
	requestPoller    *poller.Poller
	consensusHandler chainconsensus.Handler
	oracle           core.Oracle
	id               string
}

var _ solcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return solcapserver.NewClientServer(&capabilityGRPCService{lggr: s.Logger.Named(CapabilityName), limitsFactory: s.LimitsFactory})
	})
}

func (c *capabilityGRPCService) ChainSelector() uint64 {
	return c.chainSelector
}

func (c *capabilityGRPCService) Start(_ context.Context) error {
	c.lggr.Infof("Start %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) Close() error {
	c.lggr.Infof("Closing %s", CapabilityName)

	var closers []io.Closer
	if c.oracle != nil {
		closers = append(closers, closeFunc(func() error {
			c.lggr.Info("Closing oracle factory")
			err := c.oracle.Close(context.Background())
			if err != nil {
				return err
			}
			c.lggr.Info("Closed oracle factory")
			return nil
		}))
	}
	if c.requestPoller != nil {
		closers = append(closers, c.requestPoller)
	}
	if c.consensusHandler != nil {
		closers = append(closers, c.consensusHandler)
	}
	if c.triggerService != nil {
		closers = append(closers, c.triggerService)
	}
	return services.CloseAll(closers...)
}

func (c *capabilityGRPCService) AckEvent(ctx context.Context, triggerID string, eventID string, method string) caperrors.Error {
	return c.triggerService.AckEvent(ctx, triggerID, eventID)
}

func (c *capabilityGRPCService) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *capabilityGRPCService) Name() string {
	return c.lggr.Name()
}

func (c *capabilityGRPCService) Description() string {
	return "Contains Solana chain functionalities"
}

func (c *capabilityGRPCService) Ready() error {
	return nil
}
func (c *capabilityGRPCService) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	c.lggr.Infof("Initialising %s", CapabilityName)

	cfg, err := c.unmarshalConfig(dependencies.Config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	relayID := types.NewRelayID(cfg.Network, cfg.ChainID)

	relayer, err := dependencies.RelayerSet.Get(ctx, relayID)
	if err != nil {
		return fmt.Errorf("failed to fetch relayer for chainID %s from relayerSet: %w", cfg.ChainID, err)
	}

	solService, err := relayer.Solana()
	if err != nil {
		return fmt.Errorf("failed to get solana service: %w", err)
	}
	err = c.setSelector(cfg)
	if err != nil {
		return err
	}

	c.id = "solana" + ":ChainSelector:" + strconv.FormatUint(c.chainSelector, 10) + "@1.0.0"

	var chainInfo types.ChainInfo
	// protection for e2e tests when we run against local validator
	if !cfg.IsLocal {
		chainInfo, err = relayer.GetChainInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch chain info for chainID %s from relayer: %w", cfg.ChainID, err)
		}
	}

	messageBuilder := monitoring.NewMessageBuilder(chainInfo, c.CapabilityInfo, cfg.Transmitter.String())

	client := beholder.GetClient().ForName("solana_capability")
	metrics, err := monitoring.NewMetrics()
	if err != nil {
		return fmt.Errorf("failed to create metrics: %w", err)
	}
	processor, err := monitoring.NewProcessor(beholder.NewProtoEmitter(c.lggr, &client, schemaBasePath), metrics)
	if err != nil {
		return fmt.Errorf("failed to create monitoring proto processor: %w", err)
	}

	var scheduler ts.TransmissionScheduler
	if cfg.DeltaStage > 0 {
		// TODO(CRE-4409 follow-up): use dependencies.CapabilityDonID once Solana
		// starts emitting KeyDonID events.
		// Until then, passing 0 preserves the legacy "first matched DON" behavior.
		myDON, err := ts.InitMyDON(ctx, dependencies.CapabilityRegistry, c.id, 0, c.lggr, false)
		if err != nil {
			return fmt.Errorf("failed to init DON: %w", err)
		}
		c.DON = &myDON
		c.lggr.Debugw("Initialised DON", "donID", c.DON.ID, "donName", c.DON.Name, "members", len(c.DON.Members), "F", c.DON.F)
		scheduler, err = ts.InitialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage, c.lggr, c.DON, false)
		if err != nil {
			return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
		}
		c.lggr.Debugw("Initialised transmission scheduler", "deltaStage", cfg.DeltaStage)
	} else {
		c.lggr.Infow("DeltaStage not configured, transmission scheduling disabled")
	}

	var toStart []interface{ Start(context.Context) error }
	if cfg.ReadsEnabled {
		consensusMetrics, err := consMetrics.NewConsensusMetrics(chainInfo)
		if err != nil {
			return fmt.Errorf("failed to create solana consensus metrics: %w", err)
		}
		c.requestPoller = poller.NewPoller(c.lggr, consensusMetrics, cfg.ObservationPollerWorkersCount, cfg.ObservationPollPeriod)
		c.consensusHandler = chainconsensus.NewHandler(c.lggr, c.requestPoller, consensusMetrics, cfg.UnknownRequestsTTL)
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
			ReportingPluginFactoryService: oracle.NewReportingPluginFactory(logger.Sugared(c.lggr), c.consensusHandler, noopBlocksProvider{}, consensusMetrics),
			ContractTransmitter:           oracle.NewContractTransmitter(c.lggr, c.consensusHandler),
		})
		if err != nil {
			return fmt.Errorf("error when creating oracle: %w", err)
		}
		toStart = append(toStart, c.requestPoller, c.consensusHandler, c.oracle)
	} else {
		c.lggr.Warn("Initialising solana oracle required for chain reads is disabled")
	}

	c.Solana, err = actions.NewSolana(ctx, cfg, solService, messageBuilder, processor, c.lggr, c.limitsFactory, scheduler, c.chainSelector, c.consensusHandler)
	if err != nil {
		return err
	}

	c.triggerService, err = trigger.NewLogTriggerService(trigger.LogTriggerServiceOpts{
		SolanaService:     solService,
		Logger:            c.lggr,
		BeholderProcessor: processor,
		MessageBuilder:    messageBuilder,
		Triggers:          trigger.NewSolanaLogTriggerStore(),
		LimitsFactory:     c.limitsFactory,
		TriggerEventStore: dependencies.TriggerEventStore,
		CapabilityID:      c.id,
		OrgResolver:       dependencies.OrgResolver,
	})
	if err != nil {
		return fmt.Errorf("failed to create log trigger service: %w", err)
	}

	toStart = append(toStart, c.triggerService)
	for _, service := range toStart {
		if err := service.Start(ctx); err != nil {
			return err
		}
	}

	c.lggr.Infof("Successfully initialised %s", CapabilityName)
	return nil
}

func (s *capabilityGRPCService) setSelector(cfg *config.Config) error {
	// When we run against a local validator (e.g. local CRE) we can't resolve chain selector
	// since ChainID is always different
	if cfg.IsLocal {
		s.chainSelector = chain_selectors.TEST_22222222222222222222222222222222222222222222.Selector
		return nil
	}

	cs, ok := chain_selectors.SolanaChainIdToChainSelector()[cfg.ChainID]
	if !ok {
		return fmt.Errorf("chain selector not found for chainID: %s", cfg.ChainID)
	}

	s.chainSelector = cs

	return nil
}

func (c *capabilityGRPCService) unmarshalConfig(configStr string) (*config.Config, error) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Solana capability config: %s err: %w", configStr, err)
	}

	if cfg.ObservationPollerWorkersCount == 0 {
		cfg.ObservationPollerWorkersCount = 10
		c.lggr.Infof("ObservationPollerWorkersCount is zero, setting to %d.", cfg.ObservationPollerWorkersCount)
	}

	if cfg.ObservationPollPeriod == 0 {
		cfg.ObservationPollPeriod = 200 * time.Millisecond // 1/2 of Solana's 400 ms block time to ensure volatile request makes observations for every block
		c.lggr.Infof("ObservationPollPeriod is zero, setting to %s.", cfg.ObservationPollPeriod)
	}

	if cfg.UnknownRequestsTTL == 0 {
		cfg.UnknownRequestsTTL = 10 * time.Second
		c.lggr.Infof("UnknownRequestsTTL is zero, setting to %s.", cfg.UnknownRequestsTTL)
	}

	return &cfg, nil
}

func (s *capabilityGRPCService) RegisterLogTrigger(
	ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solana.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*solana.Log], caperrors.Error) {
	return s.triggerService.RegisterLogTrigger(ctx, triggerID, metadata, input)
}

func (s *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solana.FilterLogTriggerRequest) caperrors.Error {
	return s.triggerService.UnregisterLogTrigger(ctx, triggerID, metadata, input)
}

type closeFunc func() error

func (f closeFunc) Close() error {
	return f()
}
