package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	chain_selectors "github.com/smartcontractkit/chain-selectors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptoscapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/height"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	consMetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/poller"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
)

const (
	CapabilityName    = "aptos"
	CapabilityVersion = "1.0.0"

	defaultObservationPollerWorkers = 10
	defaultObservationPollPeriod    = 2 * time.Second
	defaultUnknownRequestsTTL       = 10 * time.Second
	defaultChainHeightPollPeriod    = time.Second
)

func capabilityID(chainSelector uint64) string {
	return CapabilityName + ":ChainSelector:" + strconv.FormatUint(chainSelector, 10) + "@" + CapabilityVersion
}

// capabilityGRPCService is the top-level server wrapping the Aptos capability.
// It implements loop.StandardCapabilities.
type capabilityGRPCService struct {
	chainSelector uint64
	capability
	lggr          logger.Logger
	capRegistry   core.CapabilitiesRegistry
	limitsFactory limits.Factory
	stopCh        chan struct{}
}

type capability struct {
	*actions.Aptos
	requestPoller    *poller.Poller
	consensusHandler *chainconsensus.Handler
	oracle           core.Oracle
	heightProvider   *height.Provider
}

var _ aptoscapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return aptoscapserver.NewClientServer(&capabilityGRPCService{
			lggr:          s.Logger.Named(CapabilityName),
			limitsFactory: s.LimitsFactory,
			stopCh:        make(chan struct{}),
		})
	})
}

// --- loop.StandardCapabilities / services.Service ---

func (c *capabilityGRPCService) ChainSelector() uint64 {
	return c.chainSelector
}

func (c *capabilityGRPCService) Start(ctx context.Context) error {
	c.lggr.Infof("Start %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) Close() error {
	c.lggr.Infof("Closing %s", CapabilityName)

	var closeErr error

	if c.capRegistry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.capRegistry.Remove(ctx, capabilityID(c.chainSelector)); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}

	if c.stopCh != nil {
		close(c.stopCh)
	}

	if c.Aptos != nil {
		closeErr = errors.Join(closeErr, c.Aptos.Close())
	}
	if c.requestPoller != nil {
		closeErr = errors.Join(closeErr, c.requestPoller.Close())
	}
	if c.consensusHandler != nil {
		closeErr = errors.Join(closeErr, c.consensusHandler.Close())
	}
	if c.oracle != nil {
		closeErr = errors.Join(closeErr, c.oracle.Close(context.Background()))
	}
	if c.heightProvider != nil {
		closeErr = errors.Join(closeErr, c.heightProvider.Close())
	}

	return closeErr
}

func (c *capabilityGRPCService) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *capabilityGRPCService) Name() string {
	return c.lggr.Name()
}

func (c *capabilityGRPCService) Description() string {
	return "Contains Aptos chain functionalities"
}

func (c *capabilityGRPCService) Ready() error {
	return nil
}

func (c *capabilityGRPCService) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	c.lggr.Infof("Initialising %s", CapabilityName)
	c.capRegistry = dependencies.CapabilityRegistry

	cfg, err := c.unmarshalConfig(dependencies.Config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	relayID := types.NewRelayID(cfg.Network, cfg.ChainID)

	relayer, err := dependencies.RelayerSet.Get(ctx, relayID)
	if err != nil {
		return fmt.Errorf("failed to fetch relayer for chainID %s from relayerSet: %w", cfg.ChainID, err)
	}

	aptosService, err := relayer.Aptos()
	if err != nil {
		return fmt.Errorf("failed to get aptos service: %w", err)
	}

	if err := c.setSelector(cfg); err != nil {
		return err
	}

	chainInfo, err := relayer.GetChainInfo(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch chain info for chainID %s from relayer: %w", cfg.ChainID, err)
	}

	consensusMetrics, err := consMetrics.NewConsensusMetrics(chainInfo)
	if err != nil {
		return fmt.Errorf("failed to create aptos consensus metrics: %w", err)
	}
	c.requestPoller = poller.NewPoller(c.lggr, consensusMetrics, defaultObservationPollerWorkers, defaultObservationPollPeriod)
	c.consensusHandler = chainconsensus.NewHandler(c.lggr, c.requestPoller, consensusMetrics, defaultUnknownRequestsTTL)
	c.heightProvider = height.NewProvider(c.lggr, defaultChainHeightPollPeriod, aptosService)

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
		ReportingPluginFactoryService: oracle.NewReportingPluginFactory(logger.Sugared(c.lggr), c.consensusHandler, c.heightProvider, consensusMetrics),
		ContractTransmitter:           oracle.NewContractTransmitter(c.lggr, c.consensusHandler),
	})
	if err != nil {
		return fmt.Errorf("error when creating oracle: %w", err)
	}

	c.Aptos, err = actions.NewAptos(cfg, aptosService, c.consensusHandler, c.lggr, c.limitsFactory)
	if err != nil {
		return fmt.Errorf("failed to create Aptos actions: %w", err)
	}

	startServices := []interface{ Start(context.Context) error }{c.consensusHandler, c.requestPoller, c.oracle, c.heightProvider}
	for _, service := range startServices {
		if err := service.Start(ctx); err != nil {
			return err
		}
	}

	c.lggr.Infof("Successfully initialised %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) Infos(ctx context.Context) ([]capabilities.CapabilityInfo, error) {
	info, err := capabilities.NewCapabilityInfo(
		capabilityID(c.chainSelector),
		capabilities.CapabilityTypeAction,
		"Contains Aptos chain functionalities",
	)
	if err != nil {
		return nil, err
	}
	return []capabilities.CapabilityInfo{info}, nil
}

func (c *capabilityGRPCService) AccountAPTBalance(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.AccountAPTBalanceRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.AccountAPTBalanceReply], caperrors.Error) {
	// TODO(aptos): implement account balance action; intentionally left out of the
	// local-CRE-minimal scope while Aptos View is upstreamed first.
	return nil, c.unimplementedMethod("AccountAPTBalance")
}

func (c *capabilityGRPCService) TransactionByHash(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.TransactionByHashRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.TransactionByHashReply], caperrors.Error) {
	// TODO(aptos): wire capability-level TransactionByHash to the relayer implementation
	// with the same consensus/locking semantics used by Aptos View in this capability.
	return nil, c.unimplementedMethod("TransactionByHash")
}

func (c *capabilityGRPCService) AccountTransactions(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.AccountTransactionsRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.AccountTransactionsReply], caperrors.Error) {
	// TODO(aptos): wire capability-level AccountTransactions to the existing relayer
	// implementation with explicit consensus/locking semantics for deterministic reads.
	return nil, c.unimplementedMethod("AccountTransactions")
}

func (c *capabilityGRPCService) unimplementedMethod(method string) caperrors.Error {
	if c.Aptos == nil {
		return caperrors.NewPublicSystemError(fmt.Errorf("aptos capability not initialized"), caperrors.Unknown)
	}
	return caperrors.NewPublicSystemError(fmt.Errorf("%s is not implemented", method), caperrors.Unknown)
}

func (c *capabilityGRPCService) setSelector(cfg *config.Config) error {
	if cfg.IsLocal {
		// Aptos local CRE uses chain-id 4 and the standard aptos-localnet selector.
		c.chainSelector = chain_selectors.APTOS_LOCALNET.Selector
		return nil
	}

	chainID, err := strconv.ParseUint(cfg.ChainID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse chainID: %w", err)
	}
	cs, ok := chain_selectors.AptosChainIdToChainSelector()[chainID]
	if !ok {
		return fmt.Errorf("chain selector not found for chainID: %s", cfg.ChainID)
	}
	c.chainSelector = cs
	return nil
}

func (c *capabilityGRPCService) unmarshalConfig(configStr string) (*config.Config, error) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Aptos capability config: %s err: %w", configStr, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Aptos capability config: %w", err)
	}
	return &cfg, nil
}
