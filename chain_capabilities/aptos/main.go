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
			c.lggr.Errorw("TestingAptosWriteCap: failed to remove from capability registry", "error", err)
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
	c.lggr.Infow("TestingAptosWriteCap: Initialising capability",
		"capability", CapabilityName,
		"rawConfig", dependencies.Config,
		"hasCapabilityRegistry", dependencies.CapabilityRegistry != nil,
		"hasRelayerSet", dependencies.RelayerSet != nil,
	)

	cfg, err := c.unmarshalConfig(dependencies.Config)
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to unmarshal config", "error", err)
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}
	c.lggr.Infow("TestingAptosWriteCap: Unmarshalled config",
		"network", cfg.Network,
		"chainID", cfg.ChainID,
		"isLocal", cfg.IsLocal,
		"deltaStage", cfg.DeltaStage,
		"creForwarderAddress", fmt.Sprintf("%x", cfg.CREForwarderAddress),
	)

	relayID := types.NewRelayID(cfg.Network, cfg.ChainID)
	c.lggr.Infow("TestingAptosWriteCap: Created relay ID", "relayID", relayID)

	relayer, err := dependencies.RelayerSet.Get(ctx, relayID)
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to fetch relayer", "chainID", cfg.ChainID, "relayID", relayID, "error", err)
		return fmt.Errorf("failed to fetch relayer for chainID %s from relayerSet: %w", cfg.ChainID, err)
	}
	c.lggr.Infow("TestingAptosWriteCap: Fetched relayer from relayer set", "relayID", relayID)

	aptosService, err := relayer.Aptos()
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to get aptos service from relayer", "error", err)
		return fmt.Errorf("failed to get aptos service: %w", err)
	}
	c.lggr.Infow("TestingAptosWriteCap: Got Aptos service from relayer")

	if err := c.setSelector(cfg); err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to set chain selector", "error", err)
		return err
	}
	c.capRegistry = dependencies.CapabilityRegistry
	c.id = capabilityID(c.chainSelector)
	c.lggr.Infow("TestingAptosWriteCap: Set chain selector and capability ID",
		"chainSelector", c.chainSelector,
		"capabilityID", c.id,
	)

	if !cfg.IsLocal {
		if err := c.initMyDON(ctx, dependencies.CapabilityRegistry); err != nil {
			c.lggr.Errorw("TestingAptosWriteCap: failed to init DON", "error", err)
			return fmt.Errorf("failed to init DON: %w", err)
		}
		c.lggr.Infow("TestingAptosWriteCap: Initialised DON", "donID", c.DON.ID, "donName", c.DON.Name, "members", len(c.DON.Members), "F", c.DON.F)
	} else {
		c.lggr.Infow("TestingAptosWriteCap: Skipping DON init (isLocal=true)")
	}

	var p2pConfig map[string]string
	if !cfg.IsLocal {
		p2pConfig, err = c.fetchP2PConfig(ctx, dependencies.CapabilityRegistry, c.lggr)
		if err != nil {
			c.lggr.Errorw("TestingAptosWriteCap: failed to fetch p2p config", "error", err)
			return fmt.Errorf("failed to fetch p2p config from capability registry: %w", err)
		}
		c.lggr.Infow("TestingAptosWriteCap: Fetched p2p config from capability registry", "entries", len(p2pConfig), "p2pConfig", p2pConfig)
	} else {
		c.lggr.Infow("TestingAptosWriteCap: Skipping p2p config fetch (isLocal=true)")
	}

	var scheduler actions.TransmissionScheduler
	// if cfg.DeltaStage > 0 {
	scheduler, err = c.initialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage, cfg.IsLocal, p2pConfig)
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to initialize transmission scheduler", "error", err)
		return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
	}
	c.lggr.Infow("TestingAptosWriteCap: Initialised transmission scheduler", "deltaStage", cfg.DeltaStage)
	// }
	// else {
	// 	c.lggr.Infow("TestingAptosWriteCap: DeltaStage not configured, transmission scheduling disabled")
	// }

	c.Aptos, err = actions.NewAptos(cfg, p2pConfig, aptosService, c.lggr, limits.Factory{Logger: c.lggr}, scheduler)
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to create Aptos actions", "error", err)
		return fmt.Errorf("failed to create Aptos actions: %w", err)
	}
	c.lggr.Infow("TestingAptosWriteCap: Created Aptos actions")

	c.lggr.Infof("TestingAptosWriteCap: Successfully initialised %s", CapabilityName)
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
		c.chainSelector = chain_selectors.APTOS_LOCALNET.Selector
		return nil
	}

	chainID, err := strconv.ParseUint(cfg.ChainID, 10, 64)
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to parse chainID", "chainID", cfg.ChainID, "error", err)
		return fmt.Errorf("failed to parse chainID: %w", err)
	}
	cs, ok := chain_selectors.AptosChainIdToChainSelector()[chainID]
	if !ok {
		c.lggr.Errorw("TestingAptosWriteCap: chain selector not found", "chainID", cfg.ChainID)
		return fmt.Errorf("chain selector not found for chainID: %s", cfg.ChainID)
	}
	c.chainSelector = cs
	return nil
}

func (c *capabilityGRPCService) initMyDON(ctx context.Context, registry core.CapabilitiesRegistry) error {
	localNode, err := registry.LocalNode(ctx)
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed to get local node", "error", err)
		return fmt.Errorf("failed to receiver local node: %w", err)
	}

	var dons []capabilities.DON

	donsWithNodes, err := registry.DONsForCapability(ctx, c.id)
	if err != nil {
		c.lggr.Errorw("TestingAptosWriteCap: failed getting DONs for capability", "capabilityID", c.id, "error", err)
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
		c.lggr.Errorw("TestingAptosWriteCap: no DON found for local peer", "peerID", localNode.PeerID.String(), "capabilityID", c.id)
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
		c.lggr.Errorw("TestingAptosWriteCap: failed to get local node for transmission scheduler", "error", err)
		return actions.TransmissionScheduler{}, fmt.Errorf("failed to get local node: %w", err)
	}

	if c.DON == nil {
		c.lggr.Errorw("TestingAptosWriteCap: DON is nil when initialising transmission scheduler")
		return actions.TransmissionScheduler{}, errors.New("capabilityInfo DON is nil")
	}

	if len(c.DON.Members) == 0 {
		c.lggr.Errorw("TestingAptosWriteCap: DON has no members when initialising transmission scheduler")
		return actions.TransmissionScheduler{}, errors.New("capabilityInfo DON is empty")
	}

	var donPeerIDs []p2ptypes.PeerID
	myPeerID := localNode.PeerID
	donPeerIDs = append(donPeerIDs, c.DON.Members...)

	if myPeerID == nil {
		c.lggr.Errorw("TestingAptosWriteCap: local node peer ID is nil")
		return actions.TransmissionScheduler{}, fmt.Errorf("local node peer ID is nil")
	}
	if len(donPeerIDs) == 0 {
		c.lggr.Errorw("TestingAptosWriteCap: DON members list is empty")
		return actions.TransmissionScheduler{}, fmt.Errorf("DON members list is empty")
	}

	found := slices.Contains(donPeerIDs, *myPeerID)
	if !found {
		c.lggr.Errorw("TestingAptosWriteCap: local peer not in DON members", "myPeerID", myPeerID.String(), "donMembers", len(donPeerIDs))
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

func (c *capabilityGRPCService) fetchP2PConfig(ctx context.Context, capRegistry core.CapabilitiesRegistry, lggr logger.Logger) (map[string]string, error) {
	lggr.Infow("TestingAptosWriteCap: fetchP2PConfig called", "capabilityID", c.id, "donID", c.DON.ID)

	capCfg, err := capRegistry.ConfigForCapability(ctx, c.id, c.DON.ID)
	if err != nil {
		lggr.Errorw("TestingAptosWriteCap: ConfigForCapability failed", "capabilityID", c.id, "donID", c.DON.ID, "error", err)
		return nil, fmt.Errorf("failed to get capability config: %w", err)
	}
	lggr.Infow("TestingAptosWriteCap: ConfigForCapability returned",
		"hasDefaultConfig", capCfg.DefaultConfig != nil,
		"hasRemoteTriggerConfig", capCfg.RemoteTriggerConfig != nil,
		"hasRemoteExecutableConfig", capCfg.RemoteExecutableConfig != nil,
		"methodConfigCount", len(capCfg.CapabilityMethodConfig),
		"hasSpecConfig", capCfg.SpecConfig != nil,
		"localOnly", capCfg.LocalOnly,
	)

	if capCfg.DefaultConfig == nil {
		lggr.Errorw("TestingAptosWriteCap: DefaultConfig is nil", "capabilityID", c.id, "donID", c.DON.ID)
		return nil, fmt.Errorf("capability config is nil for capability %s don %d", c.id, c.DON.ID)
	}

	lggr.Infow("TestingAptosWriteCap: DefaultConfig raw", "defaultConfig", fmt.Sprintf("%v", capCfg.DefaultConfig))

	var p2pConfig map[string]string
	if err := capCfg.DefaultConfig.UnwrapTo(&p2pConfig); err != nil {
		lggr.Errorw("TestingAptosWriteCap: failed to UnwrapTo p2p config", "defaultConfig", fmt.Sprintf("%v", capCfg.DefaultConfig), "error", err)
		return nil, fmt.Errorf("failed to unwrap capability config: %w", err)
	}
	lggr.Infow("TestingAptosWriteCap: Unwrapped p2p config", "entries", len(p2pConfig), "p2pConfig", p2pConfig)

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
