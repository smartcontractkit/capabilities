package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	chainselectors "github.com/smartcontractkit/chain-selectors"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/libs/loopserver"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	solcapserver "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

const (
	CapabilityName = "solana"
)

type capabilityGRPCService struct {
	capabilities.CapabilityInfo
	chainSelector uint64
	capability
	lggr logger.Logger
}

type capability struct {
	*actions.Solana
}

var _ solcapserver.ClientCapability = &capabilityGRPCService{}

func main() {
	loopserver.ServeNew(CapabilityName, func(s *loop.Server) loop.StandardCapabilities {
		return solcapserver.NewClientServer(&capabilityGRPCService{lggr: s.Logger.Named(CapabilityName)})
	})
}

func (c *capabilityGRPCService) Initialise(ctx context.Context, dependencies core.StandardCapabilitiesDependencies) error {
	c.lggr.Infof("Initialising %s", CapabilityName)

	cfg, err := c.unmarshalConfig(dependencies.Config)
	if err != nil {
		return fmt.Errorf("failed to unmarshal config: %w", err)
	}

	c.lggr.Infof("Initialising %s, ChainId: %s, Network: %s", CapabilityName, cfg.ChainID, cfg.Network)

	relayID := types.NewRelayID(cfg.Network, cfg.ChainID)
	relayer, err := dependencies.RelayerSet.Get(ctx, relayID)
	if err != nil {
		return fmt.Errorf("failed to fetch relayer for chainID %s from relayerSet: %w", cfg.ChainID, err)
	}

	// Get chain selector for Solana
	cs, ok := chainselectors.SolanaChainIdToChainSelector()[cfg.ChainID]
	if !ok {
		return fmt.Errorf("chain selector not found for chainID: %s", cfg.ChainID)
	}

	c.chainSelector = cs

	solanaRelayer, err := relayer.Solana()
	if err != nil {
		return fmt.Errorf("failed to init solana relayer for chainID %s from relayer: %w", cfg.ChainID, err)
	}

	c.Solana, err = actions.NewSolana(*cfg, solanaRelayer, c.lggr, c.chainSelector)
	if err != nil {
		return fmt.Errorf("failed to init solana actions for chainID %s: %w", cfg.ChainID, err)
	}

	c.lggr.Infof("Successfully initialised %s", CapabilityName)
	return nil
}

func (c *capabilityGRPCService) unmarshalConfig(configStr string) (*config.Config, error) {
	var cfg config.Config
	if err := json.Unmarshal([]byte(configStr), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse Solana capability config: %w", err)
	}

	if cfg.ChainID == "" {
		return nil, fmt.Errorf("chainId is required")
	}

	if cfg.Network == "" {
		cfg.Network = "solana"
	}

	return &cfg, nil
}

func (c *capabilityGRPCService) Start(_ context.Context) error {
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

func (c *capabilityGRPCService) ChainSelector() uint64 {
	return c.chainSelector
}

func (c *capabilityGRPCService) Description() string {
	return "Contains Solana chain functionalities"
}

func (c *capabilityGRPCService) Ready() error {
	return nil
}

func (c *capabilityGRPCService) RegisterToWorkflow(_ context.Context, _ capabilities.RegisterToWorkflowRequest) error {
	// Not implemented for Solana capability
	return nil
}

func (c *capabilityGRPCService) UnregisterFromWorkflow(_ context.Context, _ capabilities.UnregisterFromWorkflowRequest) error {
	// Not implemented for Solana capability
	return nil
}

// RegisterLogTrigger registers a log trigger for Solana.
func (c *capabilityGRPCService) RegisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solcap.FilterLogTriggerRequest) (<-chan capabilities.TriggerAndId[*solcap.Log], caperrors.Error) {
	return nil, caperrors.NewPublicSystemError(errors.New("log trigger not implemented for Solana"), caperrors.Unknown)
}

// UnregisterLogTrigger unregisters a log trigger for Solana.
func (c *capabilityGRPCService) UnregisterLogTrigger(ctx context.Context, triggerID string, metadata capabilities.RequestMetadata, input *solcap.FilterLogTriggerRequest) caperrors.Error {
	return caperrors.NewPublicSystemError(errors.New("log trigger not implemented for Solana"), caperrors.Unknown)
}

// WriteReport writes a report to the Solana chain.
func (c *capabilityGRPCService) WriteReport(ctx context.Context, metadata capabilities.RequestMetadata, input *solcap.WriteReportRequest) (*capabilities.ResponseAndMetadata[*solcap.WriteReportReply], caperrors.Error) {
	return nil, caperrors.NewPublicSystemError(errors.New("write report not implemented for Solana"), caperrors.Unknown)
}
