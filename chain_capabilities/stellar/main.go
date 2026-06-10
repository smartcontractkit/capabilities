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

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/config"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	consmetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/poller"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	stellarcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	CapabilityName    = "stellar"
	CapabilityVersion = "1.0.0"

	// Default values for optional Stellar consensus/read settings when not provided in config.
	defaultObservationPollPeriod = 3 * time.Second
	defaultPollerWorkersCount    = 10
	defaultUnknownRequestsTTL    = 10 * time.Second
)

func capabilityID(chainSelector uint64) string {
	return CapabilityName + ":ChainSelector:" + strconv.FormatUint(chainSelector, 10) + "@" + CapabilityVersion
}

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
	}, loop.WithOtelViews(consmetrics.MetricViews()))
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

	if err = c.setSelector(cfg); err != nil {
		return err
	}
	c.id = capabilityID(c.chainSelector)

	var chainInfo types.ChainInfo
	if !cfg.IsLocal {
		chainInfo, err = relayer.GetChainInfo(ctx)
		if err != nil {
			return fmt.Errorf("failed to fetch chain info for chainID %s from relayer: %w", cfg.ChainID, err)
		}
	}

	var scheduler ts.TransmissionScheduler
	if cfg.DeltaStage > 0 {
		myDON, err := ts.InitMyDON(ctx, dependencies.CapabilityRegistry, c.id, c.lggr, cfg.IsLocal)
		if err != nil {
			return fmt.Errorf("failed to init DON: %w", err)
		}
		c.DON = &myDON
		scheduler, err = ts.InitialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage, c.lggr, c.DON, cfg.IsLocal)
		if err != nil {
			return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
		}
	}

	consensusMetrics, err := consmetrics.NewConsensusMetrics(chainInfo)
	if err != nil {
		return fmt.Errorf("failed to create stellar consensus metrics: %w", err)
	}
	c.requestPoller = poller.NewPoller(c.lggr, consensusMetrics, cfg.ObservationPollerWorkersCount, cfg.ObservationPollPeriod)
	c.consensusHandler = chainconsensus.NewHandler(c.lggr, c.requestPoller, consensusMetrics, cfg.UnknownRequestsTTL)
	c.oracle, err = dependencies.OracleFactory.NewOracle(ctx, core.OracleArgs{
		LocalConfig: ocrtypes.LocalConfig{
			BlockchainTimeout:                  20 * time.Second,
			ContractConfigTrackerPollInterval:  10 * time.Second,
			ContractConfigConfirmations:        1,
			ContractTransmitterTransmitTimeout: 10 * time.Second,
			DatabaseTimeout:                    10 * time.Second,
			ContractConfigLoadTimeout:          10 * time.Second,
			DefaultMaxDurationInitialization:   10 * time.Second,
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

	messageBuilder := commonmon.NewMessageBuilder(chainInfo, c.CapabilityInfo, nodeAddress)
	c.Stellar, err = actions.NewStellar(
		stellarService,
		cfg.CREForwarderAddress,
		c.lggr,
		c.limitsFactory,
		scheduler,
		c.chainSelector,
		c.consensusHandler,
		messageBuilder,
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
		c.chainSelector = chain_selectors.STELLAR_LOCALNET.Selector
		return nil
	}

	cs, ok := chain_selectors.StellarChainIdToChainSelector()[cfg.ChainID]
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

	return &cfg, nil
}
