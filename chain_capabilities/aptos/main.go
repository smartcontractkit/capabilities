package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	chain_selectors "github.com/smartcontractkit/chain-selectors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	aptoscapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	CapabilityName    = "aptos"
	CapabilityVersion = "1.0.0"
)

func capabilityID(chainSelector uint64) string {
	return CapabilityName + ":ChainSelector:" + strconv.FormatUint(chainSelector, 10) + "@" + CapabilityVersion
}

// capabilityGRPCService is the top-level server wrapping the Aptos capability.
// It implements loop.StandardCapabilities.
type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	chainSelector uint64
	capability
	lggr          logger.Logger
	capRegistry   core.CapabilitiesRegistry
	limitsFactory limits.Factory
	stopCh        chan struct{}
}

type capability struct {
	*actions.Aptos
	id string
}

var _ aptoscapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return aptoscapserver.NewClientServer(&capabilityGRPCService{
			lggr:          s.Logger.Named(CapabilityName),
			stopCh:        make(chan struct{}),
			limitsFactory: s.LimitsFactory,
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

	if c.capRegistry != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := c.capRegistry.Remove(ctx, capabilityID(c.chainSelector)); err != nil {
			return err
		}
	}

	if c.stopCh != nil {
		close(c.stopCh)
	}
	return nil
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
	c.capRegistry = dependencies.CapabilityRegistry

	c.id = capabilityID(c.chainSelector)

	if !cfg.IsLocal {
		if err := c.initMyDON(ctx, dependencies.CapabilityRegistry); err != nil {
			return fmt.Errorf("failed to init DON: %w", err)
		}
	}

	p2pConfig, err := c.fetchP2PConfig(ctx, dependencies.CapabilityRegistry)
	if err != nil {
		return fmt.Errorf("failed to fetch p2p config from capability registry: %w", err)
	}
	c.lggr.Infow("Fetched p2p config", "entries", len(p2pConfig))

	var scheduler actions.TransmissionScheduler
	if cfg.DeltaStage > 0 {
		scheduler, err = c.initialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage, cfg.IsLocal, p2pConfig)
		if err != nil {
			return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
		}
	} else {
		c.lggr.Infow("DeltaStage not configured, transmission scheduling disabled")
	}

	c.Aptos, err = actions.NewAptos(cfg, p2pConfig, aptosService, c.lggr, limits.Factory{Logger: c.lggr}, scheduler)
	if err != nil {
		return fmt.Errorf("failed to create Aptos actions: %w", err)
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

func (c *capabilityGRPCService) setSelector(cfg *config.Config) error {
	if cfg.IsLocal {
		c.chainSelector = chain_selectors.TEST_22222222222222222222222222222222222222222222.Selector
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
	p2pConfig map[string]string,
) (actions.TransmissionScheduler, error) {
	if isLocal {
		return actions.TransmissionScheduler{}, nil
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
		p2pConfig,
		deltaStage,
		c.DON.F,
		c.lggr,
	), nil
}

func (c *capabilityGRPCService) fetchP2PConfig(ctx context.Context, capRegistry core.CapabilitiesRegistry) (map[string]string, error) {
	capCfg, err := capRegistry.ConfigForCapability(ctx, c.id, c.DON.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get capability config: %w", err)
	}

	if capCfg.DefaultConfig == nil {
		return nil, fmt.Errorf("capability config is nil for capability %s don %d", c.id, c.DON.ID)
	}

	var p2pConfig map[string]string
	if err := capCfg.DefaultConfig.UnwrapTo(&p2pConfig); err != nil {
		return nil, fmt.Errorf("failed to unwrap capability config: %w", err)
	}
	return p2pConfig, nil
}

func (c *capabilityGRPCService) unmarshalConfig(configStr string) (*config.Config, error) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Aptos capability config: %s err: %w", configStr, err)
	}
	return &cfg, nil
}

// aptosExecutableCapability adapts the Aptos actions into a capabilities.ExecutableCapability
// that can be registered with the capability registry.
type aptosExecutableCapability struct {
	aptos         *actions.Aptos
	chainSelector uint64
	description   string
	stopCh        chan struct{}
	lggr          logger.Logger
}
