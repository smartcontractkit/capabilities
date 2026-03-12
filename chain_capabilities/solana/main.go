package main

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	chain_selectors "github.com/smartcontractkit/chain-selectors"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/trigger"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
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
	id string
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
	if c.triggerService != nil {
		if err := c.triggerService.Close(); err != nil {
			return fmt.Errorf("failed to close log trigger service: %w", err)
		}
	}
	return nil
}

func (c *capabilityGRPCService) AckEvent(ctx context.Context, triggerId string, eventId string, method string) errors.Error {
	return errors.NewError(fmt.Errorf("not implemented"), errors.VisibilityPublic, errors.OriginSystem, errors.Unknown)
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
		scheduler, err = c.initialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage)
		if err != nil {
			return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
		}
	} else {
		c.lggr.Infow("DeltaStage not configured, transmission scheduling disabled")
	}

	c.Solana, err = actions.NewSolana(ctx, cfg, solService, messageBuilder, processor, c.lggr, limits.Factory{Logger: c.lggr}, scheduler)
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
	})
	if err != nil {
		return fmt.Errorf("failed to create log trigger service: %w", err)
	}

	if err := c.triggerService.Start(ctx); err != nil {
		return fmt.Errorf("failed to start log trigger service: %w", err)
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

	return &cfg, nil
}

func (c *capabilityGRPCService) initMyDON(ctx context.Context, registry core.CapabilitiesRegistry) error {
	localNode, err := registry.LocalNode(ctx)
	if err != nil {
		return fmt.Errorf("failed to get local node: %w", err)
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
		return stderrors.New("failed to find don for my peer ID: " + localNode.PeerID.String())
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
) (ts.TransmissionScheduler, error) {
	err := c.initMyDON(ctx, capRegistry)
	if err != nil {
		return ts.TransmissionScheduler{}, fmt.Errorf("failed to initialize capability with my don info: %w", err)
	}

	localNode, err := capRegistry.LocalNode(ctx)
	if err != nil {
		return ts.TransmissionScheduler{}, fmt.Errorf("failed to get local node: %w", err)
	}

	if c.DON == nil {
		return ts.TransmissionScheduler{}, stderrors.New("capabilityInfo DON is nil")
	}

	if len(c.DON.Members) == 0 {
		return ts.TransmissionScheduler{}, stderrors.New("capabilityInfo DON is empty")
	}

	var donPeerIDs []p2ptypes.PeerID
	myPeerID := localNode.PeerID
	donPeerIDs = append(donPeerIDs, c.DON.Members...)

	if myPeerID == nil {
		return ts.TransmissionScheduler{}, fmt.Errorf("local node peer ID is nil")
	}
	if len(donPeerIDs) == 0 {
		return ts.TransmissionScheduler{}, fmt.Errorf("DON members list is empty")
	}

	found := slices.Contains(donPeerIDs, *myPeerID)
	if !found {
		return ts.TransmissionScheduler{}, fmt.Errorf("local peer ID %s not found in DON members", myPeerID.String())
	}

	c.lggr.Infow("Transmission scheduler initialized",
		"deltaStage", deltaStage,
		"donSize", len(donPeerIDs),
		"F", c.DON.F,
		"myPeerID", myPeerID.String(),
	)

	return ts.NewTransmissionScheduler(
		*myPeerID,
		donPeerIDs,
		deltaStage,
		c.DON.F,
		c.lggr,
	), nil
}

func (s *capabilityGRPCService) RegisterLogTrigger(
	ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solana.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*solana.Log], caperrors.Error) {
	return s.triggerService.RegisterLogTrigger(ctx, triggerID, metadata, input)
}

func (s *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solana.FilterLogTriggerRequest) caperrors.Error {
	return s.triggerService.UnregisterLogTrigger(ctx, triggerID, metadata, input)
}
