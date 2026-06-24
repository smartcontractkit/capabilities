package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	chainselectors "github.com/smartcontractkit/chain-selectors"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	consMetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/poller"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	stellarcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const CapabilityName = "stellar"

const (
	// Default values for optional Stellar consensus/read settings when not provided in config.
	defaultObservationPollPeriod    = 3 * time.Second
	defaultPollerWorkersCount       = 10
	defaultUnknownRequestsTTL       = 10 * time.Second
	defaultForwarderLookbackLedgers = int64(100)
)

// capabilityGRPCService is the top-level server wrapping the Stellar capability.
// It implements loop.StandardCapabilities.
type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	chainSelector uint64
	capability
	lggr          logger.Logger
	limitsFactory limits.Factory
}

type capability struct {
	*actions.Stellar
	requestPoller    *poller.Poller
	consensusHandler chainconsensus.Handler
	oracle           core.Oracle
	id               string
}

type closeFunc func() error

func (f closeFunc) Close() error {
	return f()
}

var _ stellarcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return stellarcapserver.NewClientServer(&capabilityGRPCService{
			lggr:          s.Logger.Named(CapabilityName),
			limitsFactory: s.LimitsFactory,
		})
	}, loop.WithOtelViews(consMetrics.MetricViews()))
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
			return c.oracle.Close(context.Background())
		}))
	}
	if c.requestPoller != nil {
		closers = append(closers, c.requestPoller)
	}
	if c.consensusHandler != nil {
		closers = append(closers, c.consensusHandler)
	}
	if c.Stellar != nil {
		closers = append(closers, c.Stellar)
	}
	return services.CloseAll(closers...)
}

func (c *capabilityGRPCService) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *capabilityGRPCService) Name() string {
	return c.lggr.Name()
}

func (c *capabilityGRPCService) Description() string {
	return "Contains Stellar chain functionalities"
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

	stellarService, err := relayer.Stellar()
	if err != nil {
		return fmt.Errorf("failed to get stellar service: %w", err)
	}
	if _, err = stellarService.GetSigningAccount(ctx); err != nil {
		return fmt.Errorf("stellar relayer has no signing account: %w", err)
	}

	if err = c.setSelector(cfg); err != nil {
		return err
	}
	c.id = CapabilityName + ":ChainSelector:" + strconv.FormatUint(c.chainSelector, 10) + "@1.0.0"

	var chainInfo types.ChainInfo
	if !cfg.IsLocal {
		chainInfo, err = relayer.GetChainInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch chain info for chainID %s from relayer: %w", cfg.ChainID, err)
		}
	}

	var scheduler ts.TransmissionScheduler
	if cfg.DeltaStage > 0 {
		myDON, err := ts.InitMyDON(ctx, dependencies.CapabilityRegistry, c.id, dependencies.CapabilityDonID, c.lggr, cfg.IsLocal)
		if err != nil {
			return fmt.Errorf("failed to init DON: %w", err)
		}
		c.DON = &myDON
		scheduler, err = ts.InitialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage, c.lggr, c.DON, cfg.IsLocal)
		if err != nil {
			return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
		}
	}

	consensusMetrics, err := consMetrics.NewConsensusMetrics(chainInfo)
	if err != nil {
		return fmt.Errorf("failed to create stellar consensus metrics: %w", err)
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

	var nodeAddress string
	if localNode, lnErr := dependencies.CapabilityRegistry.LocalNode(ctx); lnErr != nil {
		c.lggr.Warnw("Failed to resolve local node; source id will be empty in telemetry", "error", lnErr)
	} else if localNode.PeerID != nil {
		nodeAddress = localNode.PeerID.String()
	}

	metrics, err := monitoring.NewMetrics()
	if err != nil {
		return fmt.Errorf("failed to create stellar monitoring metrics: %w", err)
	}
	processor := &monitoring.Processor{
		Lggr:    c.lggr,
		Metrics: metrics,
	}

	messageBuilder := monitoring.NewMessageBuilder(chainInfo, c.CapabilityInfo, nodeAddress)
	c.Stellar, err = actions.NewStellar(
		stellarService,
		cfg.CREForwarderAddress,
		cfg.ForwarderLookbackLedgers,
		c.lggr,
		c.limitsFactory,
		scheduler,
		c.chainSelector,
		c.consensusHandler,
		messageBuilder,
		processor,
	)
	if err != nil {
		return err
	}

	for _, service := range []interface{ Start(context.Context) error }{c.requestPoller, c.consensusHandler, c.oracle} {
		if err = service.Start(ctx); err != nil {
			return err
		}
	}

	c.lggr.Infof("Successfully initialised %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) setSelector(cfg *config.Config) error {
	if cfg.IsLocal {
		c.chainSelector = chainselectors.STELLAR_LOCALNET.Selector
		return nil
	}

	cs, ok := chainselectors.StellarChainIdToChainSelector()[cfg.ChainID]
	if !ok {
		return fmt.Errorf("chain selector not found for chainID: %s", cfg.ChainID)
	}
	c.chainSelector = cs
	return nil
}

func (c *capabilityGRPCService) unmarshalConfig(configStr string) (*config.Config, error) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Stellar capability config: %s err: %w", configStr, err)
	}

	if cfg.ObservationPollerWorkersCount == 0 {
		cfg.ObservationPollerWorkersCount = defaultPollerWorkersCount
		c.lggr.Infof("ObservationPollerWorkersCount is zero, setting to %d.", cfg.ObservationPollerWorkersCount)
	}
	if cfg.ObservationPollPeriod == 0 {
		cfg.ObservationPollPeriod = defaultObservationPollPeriod
		c.lggr.Infof("ObservationPollPeriod is zero, setting to %s.", cfg.ObservationPollPeriod)
	}
	if cfg.UnknownRequestsTTL == 0 {
		cfg.UnknownRequestsTTL = defaultUnknownRequestsTTL
		c.lggr.Infof("UnknownRequestsTTL is zero, setting to %s.", cfg.UnknownRequestsTTL)
	}
	if cfg.ForwarderLookbackLedgers == 0 {
		cfg.ForwarderLookbackLedgers = defaultForwarderLookbackLedgers
		c.lggr.Infof("ForwarderLookbackLedgers is zero, setting to %d.", cfg.ForwarderLookbackLedgers)
	}

	return &cfg, nil
}
