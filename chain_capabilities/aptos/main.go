package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	chain_selectors "github.com/smartcontractkit/chain-selectors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	aptoscapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
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
	chainSelector uint64
	capability
	lggr        logger.Logger
	capRegistry core.CapabilitiesRegistry
	stopCh      chan struct{}
}

type capability struct {
	*actions.Aptos
}

var _ aptoscapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return aptoscapserver.NewClientServer(&capabilityGRPCService{
			lggr:   s.Logger.Named(CapabilityName),
			stopCh: make(chan struct{}),
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

	c.Aptos, err = actions.NewAptos(cfg, aptosService, c.lggr)
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
