package main

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
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

	// TODO: PLEX-2598 make configurable
	defaultDeltaStage = 10 * time.Second
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
	limitsFactory limits.Factory
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
	if c.Aptos != nil {
		return c.Aptos.Close()
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
	c.lggr.Debugw("Initialising capability",
		"capability", CapabilityName,
		"rawConfig", dependencies.Config,
		"hasCapabilityRegistry", dependencies.CapabilityRegistry != nil,
		"hasRelayerSet", dependencies.RelayerSet != nil,
	)

	cfg, err := c.unmarshalConfig(dependencies.Config)
	if err != nil {
		c.lggr.Errorw("failed to unmarshal config", "error", err)
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}
	c.lggr.Debugw("Unmarshalled config",
		"network", cfg.Network,
		"chainID", cfg.ChainID,
		"deltaStage", cfg.DeltaStage,
		"creForwarderAddress", fmt.Sprintf("%x", cfg.CREForwarderAddress),
	)

	relayID := types.NewRelayID(cfg.Network, cfg.ChainID)
	c.lggr.Debugw("Created relay ID", "relayID", relayID)

	relayer, err := dependencies.RelayerSet.Get(ctx, relayID)
	if err != nil {
		c.lggr.Errorw("failed to fetch relayer", "chainID", cfg.ChainID, "relayID", relayID, "error", err)
		return fmt.Errorf("failed to fetch relayer for chainID %s from relayerSet: %w", cfg.ChainID, err)
	}
	c.lggr.Debugw("Fetched relayer from relayer set", "relayID", relayID)

	aptosService, err := relayer.Aptos()
	if err != nil {
		c.lggr.Errorw("failed to get aptos service from relayer", "error", err)
		return fmt.Errorf("failed to get aptos service: %w", err)
	}
	c.lggr.Debugw("Got Aptos service from relayer")

	if err := c.setSelector(cfg); err != nil {
		c.lggr.Errorw("failed to set chain selector", "error", err)
		return err
	}
	c.id = capabilityID(c.chainSelector)
	c.lggr.Debugw("Set chain selector and capability ID",
		"chainSelector", c.chainSelector,
		"capabilityID", c.id,
	)

	myDON, err := transmission_schedule.InitMyDON(ctx, dependencies.CapabilityRegistry, c.id, c.lggr, false)
	if err != nil {
		c.lggr.Errorw("failed to init DON", "error", err)
		return fmt.Errorf("failed to init DON: %w", err)
	}
	c.DON = &myDON
	c.lggr.Debugw("Initialised DON", "donID", c.DON.ID, "donName", c.DON.Name, "members", len(c.DON.Members), "F", c.DON.F)

	p2pConfig := cfg.P2PToTransmitterMap
	if len(p2pConfig) > 0 {
		c.lggr.Debugw("p2pToTransmitterMap found in JSON config",
			"entries", len(p2pConfig), "p2pConfig", p2pConfig,
		)
	} else {
		c.lggr.Debugw("p2pToTransmitterMap not in JSON config, falling back to capReg gRPC")
		var fetchErr error
		p2pConfig, fetchErr = c.fetchP2PConfig(ctx, dependencies.CapabilityRegistry)
		if fetchErr != nil {
			c.lggr.Errorw("failed to fetch p2p config from capReg", "error", fetchErr)
			return fmt.Errorf("failed to fetch p2p config: %w", fetchErr)
		}
		c.lggr.Debugw("p2pToTransmitterMap fetched from capReg specConfig",
			"entries", len(p2pConfig), "p2pConfig", p2pConfig,
		)
	}

	if cfg.DeltaStage == 0 {
		cfg.DeltaStage = defaultDeltaStage
	}
	scheduler, err := transmission_schedule.InitialiseTransmissionScheduler(ctx, dependencies.CapabilityRegistry, cfg.DeltaStage, c.lggr, c.DON, false)
	if err != nil {
		c.lggr.Errorw("failed to initialize transmission scheduler", "error", err)
		return fmt.Errorf("failed to initialize transmission scheduler: %w", err)
	}
	c.lggr.Debugw("Initialised transmission scheduler", "deltaStage", cfg.DeltaStage)

	c.Aptos, err = actions.NewAptos(cfg, p2pConfig, aptosService, c.lggr, limits.Factory{Logger: c.lggr}, scheduler, c.chainSelector)
	if err != nil {
		c.lggr.Errorw("failed to create Aptos actions", "error", err)
		return fmt.Errorf("failed to create Aptos actions: %w", err)
	}
	c.lggr.Debugw("Created Aptos actions")

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
	chainID, err := strconv.ParseUint(cfg.ChainID, 10, 64)
	if err != nil {
		c.lggr.Errorw("failed to parse chainID", "chainID", cfg.ChainID, "error", err)
		return fmt.Errorf("failed to parse chainID: %w", err)
	}
	cs, ok := chain_selectors.AptosChainIdToChainSelector()[chainID]
	if !ok {
		c.lggr.Errorw("chain selector not found", "chainID", cfg.ChainID)
		return fmt.Errorf("chain selector not found for chainID: %s", cfg.ChainID)
	}
	c.chainSelector = cs
	return nil
}

// fetchP2PConfig fetches the p2pID-to-transmitter-address map from the on-chain
// capability registry via gRPC. It calls ConfigForCapability to obtain the
// CapabilityConfiguration, then extracts the "p2pToTransmitterMap" key from SpecConfig.
// This is the fallback path used when the JSON config (produced by buildConfigJSON)
// does not already contain the map.
func (c *capabilityGRPCService) fetchP2PConfig(ctx context.Context, registry core.CapabilitiesRegistry) (map[string]string, error) {
	c.lggr.Debugw("fetchP2PConfig: calling ConfigForCapability",
		"capabilityID", c.id, "donID", c.DON.ID,
	)

	capCfg, err := registry.ConfigForCapability(ctx, c.id, c.DON.ID)
	if err != nil {
		c.lggr.Errorw("fetchP2PConfig: ConfigForCapability failed", "error", err)
		return nil, fmt.Errorf("failed to get capability config: %w", err)
	}

	c.lggr.Debugw("fetchP2PConfig: got CapabilityConfiguration",
		"hasDefaultConfig", capCfg.DefaultConfig != nil,
		"hasSpecConfig", capCfg.SpecConfig != nil,
	)

	if capCfg.SpecConfig == nil {
		c.lggr.Errorw("fetchP2PConfig: SpecConfig is nil")
		return nil, fmt.Errorf("SpecConfig is nil for capability %s", c.id)
	}

	unwrapped, err := capCfg.SpecConfig.Unwrap()
	if err != nil {
		c.lggr.Errorw("fetchP2PConfig: failed to unwrap SpecConfig", "error", err)
		return nil, fmt.Errorf("failed to unwrap SpecConfig: %w", err)
	}

	specMap, ok := unwrapped.(map[string]any)
	if !ok {
		c.lggr.Errorw("fetchP2PConfig: SpecConfig unwrapped to unexpected type", "type", fmt.Sprintf("%T", unwrapped))
		return nil, fmt.Errorf("SpecConfig unwrapped to %T, expected map[string]any", unwrapped)
	}

	c.lggr.Debugw("fetchP2PConfig: SpecConfig keys", "keys", fmt.Sprintf("%v", slices.Collect(maps.Keys(specMap))))

	p2pRaw, exists := specMap["p2pToTransmitterMap"]
	if !exists {
		c.lggr.Errorw("fetchP2PConfig: p2pToTransmitterMap key not found in SpecConfig")
		return nil, fmt.Errorf("p2pToTransmitterMap not found in SpecConfig")
	}

	p2pAny, ok := p2pRaw.(map[string]any)
	if !ok {
		c.lggr.Errorw("fetchP2PConfig: p2pToTransmitterMap has unexpected type", "type", fmt.Sprintf("%T", p2pRaw))
		return nil, fmt.Errorf("p2pToTransmitterMap has type %T, expected map[string]any", p2pRaw)
	}

	result := make(map[string]string, len(p2pAny))
	for k, v := range p2pAny {
		s, ok := v.(string)
		if !ok {
			c.lggr.Errorw("fetchP2PConfig: non-string value in p2pToTransmitterMap", "key", k, "type", fmt.Sprintf("%T", v))
			return nil, fmt.Errorf("p2pToTransmitterMap[%s] has type %T, expected string", k, v)
		}
		result[k] = s
	}

	c.lggr.Debugw("fetchP2PConfig: extracted p2pToTransmitterMap",
		"entries", len(result), "map", result,
	)
	return result, nil
}

func (c *capabilityGRPCService) unmarshalConfig(configStr string) (*config.Config, error) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Aptos capability config: %s err: %w", configStr, err)
	}
	return &cfg, nil
}
