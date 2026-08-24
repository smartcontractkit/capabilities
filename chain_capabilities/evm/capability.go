package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	chainselectors "github.com/smartcontractkit/chain-selectors"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	commontypes2 "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/height"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/protos"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/trigger"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus"
	consMetrics "github.com/smartcontractkit/capabilities/libs/chainconsensus/metrics"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/oracle"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/poller"
	libsocr "github.com/smartcontractkit/capabilities/libs/ocr"
)

// CapabilityName is what this binary calls itself in logs and metrics. The
// capability's registered ID is not this: it carries the chain selector, since a
// workflow asks for a chain rather than for EVM in general.
const CapabilityName = "evm"

// localOCRConfig is what this capability's oracle runs under. The values are the
// node's own defaults: the rounds here agree a block height and hand back a
// chain read, so nothing about them argues for a different pace than any other
// capability's.
var localOCRConfig = ocrtypes.LocalConfig{
	BlockchainTimeout:                  20 * time.Second,
	ContractConfigTrackerPollInterval:  10 * time.Second,
	ContractConfigConfirmations:        1,
	ContractTransmitterTransmitTimeout: 10 * time.Second,
	DatabaseTimeout:                    10 * time.Second,
	ContractConfigLoadTimeout:          10 * time.Second,
	DefaultMaxDurationInitialization:   10 * time.Second,
}

// Dependencies are what an EVM capability needs from wherever it is hosted: the
// chain it reads and writes, the OCR configuration and identity it agrees under,
// and the node-shaped things it is not able to be.
//
// They are taken here rather than through an Initialise the host calls, because a
// capability that is not yet usable is only a way to be used too early: with
// these, what New returns is ready, and Start runs it.
type Dependencies struct {
	// EVMService is the chain: reads, log tracking and transaction submission.
	// Built from chainlink-evm's own components rather than reached through a
	// relayer, which is why this process needs a database and keys of its own.
	EVMService commontypes2.EVMService

	// ChainInfo names that chain, for the telemetry every message carries.
	ChainInfo commontypes2.ChainInfo

	// DonID is the capability DON this process was spawned for, which together
	// with the capability ID selects this oracle's configuration, and which the
	// trigger events are labelled with as the sending DON.
	DonID uint32

	// Registry supplies that configuration, digest included, and is also where the
	// transmission scheduler reads this DON's membership from.
	Registry core.OCRConfigRegistry

	// CapabilityRegistry resolves the DON this capability belongs to, which is
	// what staggered transmission needs to know its place in.
	CapabilityRegistry core.CapabilitiesRegistry

	// Endpoints, Offchain and Onchain come from whoever holds the node's peer:
	// the transport, and the keys this oracle signs with. TransmitAccount is the
	// account the configuration lists this node under.
	Endpoints       ocrtypes.BinaryNetworkEndpointFactory
	Offchain        ocrtypes.OffchainKeyring
	Onchain         ocr3types.OnchainKeyring[[]byte]
	TransmitAccount ocrtypes.Account

	// Bootstrappers are the peers to dial before this oracle has heard of anyone;
	// the registry says who the oracle set is, not where it is.
	Bootstrappers []commontypes.BootstrapperLocator

	// EventStore holds trigger events that have fired but not been acknowledged,
	// so a restart does not drop what was in flight.
	EventStore capabilities.EventStore

	LimitsFactory limits.Factory
	Metrics       prometheus.Registerer

	// NewOracle builds the oracle this capability runs, defaulting to a real one
	// over the configuration and networking above. A test replaces it to drive the
	// rest of the capability without a DON to agree with.
	NewOracle func(libsocr.OracleArgs) (Oracle, error)
}

// Oracle is what this capability does with a libocr oracle: run it, and stop it.
type Oracle interface {
	Start() error
	Close() error
}

// evmCapability is the EVM chain capability: the actions a workflow calls, the
// log trigger it registers, and the oracle the nodes agree a block height with.
type evmCapability struct {
	*actions.EVM

	lggr          logger.Logger
	id            string
	chainSelector uint64

	requestPoller    *poller.Poller
	consensusHandler chainconsensus.Handler
	oracle           Oracle
	triggerService   *trigger.LogTriggerService
	heightProvider   *height.Provider
}

var _ protos.ClientCapability = (*evmCapability)(nil)

// New builds the capability over the chain and identity it is given.
//
// Everything is built here and started by Start: building needs the chain and the
// configuration, and starting means joining a protocol and polling a chain -
// things to do when the process is ready to serve rather than while it is still
// being assembled.
func New(lggr logger.Logger, cfg config.Config, deps Dependencies) (*evmCapability, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	chainID, err := strconv.ParseUint(deps.ChainInfo.ChainID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("chain %q is not an EVM chain ID: %w", deps.ChainInfo.ChainID, err)
	}
	chainSelector, ok := chainselectors.EvmChainIdToChainSelector()[chainID]
	if !ok {
		return nil, fmt.Errorf("no chain selector for chain ID %d", chainID)
	}

	metrics, err := monitoring.NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics: %w", err)
	}
	processor, err := monitoring.NewProcessor(lggr, metrics)
	if err != nil {
		return nil, fmt.Errorf("failed to create monitoring proto processor: %w", err)
	}

	c := &evmCapability{
		lggr:          lggr,
		id:            CapabilityName + ":ChainSelector:" + strconv.FormatUint(chainSelector, 10) + "@1.0.0",
		chainSelector: chainSelector,
	}

	capInfo, err := capabilities.NewCapabilityInfo(c.id, capabilities.CapabilityTypeCombined, "Contains EVM chain functionalities")
	if err != nil {
		return nil, fmt.Errorf("failed to describe capability %s: %w", c.id, err)
	}
	messageBuilder := monitoring.NewMessageBuilder(deps.ChainInfo, capInfo, cfg.NodeAddress)

	consensusMetrics, err := consMetrics.NewConsensusMetrics(deps.ChainInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to create evm consensus metrics: %w", err)
	}
	c.requestPoller = poller.NewPoller(lggr, consensusMetrics, cfg.ObservationPollerWorkersCount, cfg.ObservationPollPeriod)
	c.consensusHandler = chainconsensus.NewHandler(lggr, c.requestPoller, consensusMetrics, cfg.UnknownRequestsTTL)

	scheduler, err := c.transmissionScheduler(cfg, deps)
	if err != nil {
		return nil, err
	}

	c.EVM, err = actions.NewEVM(cfg, deps.EVMService, lggr, processor, messageBuilder, c.consensusHandler,
		chainSelector, deps.LimitsFactory, scheduler)
	if err != nil {
		return nil, fmt.Errorf("failed to create EVM actions for chain %d: %w", chainID, err)
	}

	// TODO: add org resolver
	c.triggerService, err = trigger.NewLogTriggerService(deps.EVMService, trigger.NewLogTriggerStore(), lggr,
		fmt.Sprintf("%s (%d)", c.id, chainID), deps.DonID, processor, messageBuilder,
		cfg.LogTriggerPollInterval, cfg.LogTriggerSendChannelBufferSize, cfg.LogTriggerLimitQueryLogSize,
		deps.LimitsFactory, nil, deps.EventStore)
	if err != nil {
		return nil, fmt.Errorf("failed to create the log trigger: %w", err)
	}

	c.heightProvider = height.NewProvider(lggr, cfg.ChainHeightPollPeriod, deps.EVMService)

	newOracle := deps.NewOracle
	if newOracle == nil {
		newOracle = func(args libsocr.OracleArgs) (Oracle, error) { return libsocr.NewOracle(args) }
	}

	c.oracle, err = newOracle(libsocr.OracleArgs{
		CapabilityID:    c.id,
		DonID:           deps.DonID,
		Registry:        deps.Registry,
		Endpoints:       deps.Endpoints,
		Offchain:        deps.Offchain,
		Onchain:         deps.Onchain,
		TransmitAccount: deps.TransmitAccount,
		Bootstrappers:   deps.Bootstrappers,
		Plugin:          oracle.NewReportingPluginFactory(logger.Sugared(lggr), c.consensusHandler, c.heightProvider, consensusMetrics),
		Transmitter:     oracle.NewContractTransmitter(lggr, c.consensusHandler),
		LocalConfig:     localOCRConfig,
		Logger:          lggr,
		Metrics:         deps.Metrics,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create the oracle: %w", err)
	}

	return c, nil
}

// transmissionScheduler staggers what this node writes to the chain, so a DON
// answering one request does not send the same transaction from every member at
// once.
//
// It is optional: without a stage delay there is nothing to stagger, and the
// scheduler is left nil rather than built as a no-op.
func (c *evmCapability) transmissionScheduler(cfg config.Config, deps Dependencies) (ts.TransmissionScheduler, error) {
	if cfg.DeltaStage <= 0 {
		c.lggr.Infow("DeltaStage not configured, transmission scheduling disabled")
		return ts.TransmissionScheduler{}, nil
	}

	// Staggering means knowing this DON's membership and quorum, and which member
	// this node is. The authoritative DON ID is passed so that a node belonging to
	// several DONs running this capability picks the right one.
	ctx := context.Background()
	myDON, err := ts.InitMyDON(ctx, deps.CapabilityRegistry, c.id, deps.DonID, c.lggr, cfg.IsLocal)
	if err != nil {
		return ts.TransmissionScheduler{}, fmt.Errorf("failed to init DON: %w", err)
	}
	c.lggr.Debugw("Initialised DON", "donID", myDON.ID, "donName", myDON.Name, "members", len(myDON.Members), "F", myDON.F)

	scheduler, err := ts.InitialiseTransmissionScheduler(ctx, deps.CapabilityRegistry, cfg.DeltaStage, c.lggr, &myDON, cfg.IsLocal)
	if err != nil {
		return ts.TransmissionScheduler{}, fmt.Errorf("failed to initialize transmission scheduler: %w", err)
	}
	return scheduler, nil
}

// Start runs everything the capability answers with: the consensus round that
// agrees a block height, the poller feeding it, the oracle running it, and the
// log trigger reading the chain.
//
// The chain itself is not started here. It is a service of the process rather
// than of this capability - the same database and the same log poller would back
// a second capability in this binary - so whoever built it starts it.
func (c *evmCapability) Start(ctx context.Context) error {
	started := []interface{ Close() error }{}
	for _, service := range []interface {
		Start(context.Context) error
		Close() error
	}{c.consensusHandler, c.requestPoller, c.oracleService(), c.heightProvider, c.triggerService} {
		if err := service.Start(ctx); err != nil {
			// Whatever did start is stopped again: a capability that failed to start is
			// not running, and leaving half of it polling a chain would make it look like
			// it was.
			for i := len(started) - 1; i >= 0; i-- {
				if cerr := started[i].Close(); cerr != nil {
					c.lggr.Errorw("Failed to stop a service after a failed start", "err", cerr)
				}
			}
			return err
		}
		started = append(started, service)
	}

	c.lggr.Infof("Started %s", CapabilityName)
	return nil
}

func (c *evmCapability) Close() error {
	return errors.Join(
		c.EVM.Close(),
		c.requestPoller.Close(),
		c.consensusHandler.Close(),
		c.oracle.Close(),
		c.triggerService.Close(),
		c.heightProvider.Close(),
	)
}

// oracleService adapts the oracle to the Start/Close pair everything else here
// has: libocr's takes no context to start and none to stop.
func (c *evmCapability) oracleService() interface {
	Start(context.Context) error
	Close() error
} {
	return oracleService{c.oracle}
}

type oracleService struct{ oracle Oracle }

func (o oracleService) Start(context.Context) error { return o.oracle.Start() }

func (o oracleService) Close() error { return o.oracle.Close() }

func (c *evmCapability) HealthReport() map[string]error {
	return map[string]error{c.Name(): nil}
}

func (c *evmCapability) Name() string { return c.lggr.Name() }

func (c *evmCapability) Ready() error { return nil }

// ChainSelector is what makes this capability's ID say which chain it is. The
// generated server reads it, so a workflow asking for a chain reaches the binary
// running that chain.
func (c *evmCapability) ChainSelector() uint64 { return c.chainSelector }

func (c *evmCapability) Description() string { return "Contains EVM chain functionalities" }

func (c *evmCapability) RegisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *protos.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*protos.Log], caperrors.Error) {
	return c.triggerService.RegisterLogTrigger(ctx, triggerID, metadata, input)
}

func (c *evmCapability) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *protos.FilterLogTriggerRequest) caperrors.Error {
	return c.triggerService.UnregisterLogTrigger(ctx, triggerID, metadata, input)
}

func (c *evmCapability) AckEvent(ctx context.Context, triggerID string, eventID string, _ string) caperrors.Error {
	return c.triggerService.AckEvent(ctx, triggerID, eventID)
}
