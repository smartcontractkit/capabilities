package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
	"github.com/smartcontractkit/capabilities/libs/loopserver"
	chain_selectors "github.com/smartcontractkit/chain-selectors"
	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

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
	lggr          logger.Logger
	limitsFactory limits.Factory
}

type capability struct {
	*actions.Solana
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

func (c *capabilityGRPCService) Start(ctx context.Context) error {
	c.lggr.Infof("Start %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) Close() error {
	c.lggr.Infof("Closing %s", CapabilityName)
	return nil
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

	c.Solana, err = actions.NewSolana(ctx, cfg, solService, messageBuilder, processor, c.lggr, limits.Factory{Logger: c.lggr})
	if err != nil {
		return err
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

func (s *capabilityGRPCService) RegisterLogTrigger(
	ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solana.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*solana.Log], caperrors.Error) {
	return nil, actions.GetError(errors.New("unimplemented"), false)
}

func (s *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solana.FilterLogTriggerRequest) caperrors.Error {
	return actions.GetError(errors.New("unimplemented"), false)
}
