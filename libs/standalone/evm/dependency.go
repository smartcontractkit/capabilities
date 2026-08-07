package evm

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	evmclient "github.com/smartcontractkit/chainlink-evm/pkg/client"
	"github.com/smartcontractkit/chainlink-evm/pkg/config/chaintype"

	"github.com/smartcontractkit/capabilities/libs/standalone"
)

// Defaults mirror chainlink's own EVM chain defaults for a generic EVM chain, so
// a standalone binary polling the same RPCs behaves like the node does.
const (
	defaultSelectionMode              = "HighestHead"
	defaultLeaseDuration              = 0
	defaultPollFailureThreshold       = uint32(5)
	defaultPollSuccessThreshold       = uint32(0)
	defaultPollInterval               = 10 * time.Second
	defaultSyncThreshold              = uint32(5)
	defaultNoNewHeadsThreshold        = 3 * time.Minute
	defaultFinalityDepth              = uint32(50)
	defaultFinalizedBlockOffset       = uint32(0)
	defaultDeathDeclarationDelay      = 10 * time.Second
	defaultNoNewFinalizedHeads        = 0
	defaultFinalizedBlockPollInterval = 5 * time.Second
	defaultNewHeadsPollInterval       = 0
	defaultConfirmationTimeout        = 60 * time.Second
	defaultSafeDepth                  = uint32(0)
)

// Dependency returns a standalone.BootstrapDependency that resolves a dialed,
// multinode-backed EVM client.
//
// At least one --evm.http-url is required. WebSocket URLs are optional; without
// them the client polls for heads rather than subscribing, which is enough for
// view calls.
func Dependency(lggr logger.Logger) standalone.BootstrapDependency[evmclient.Client] {
	// Wrap in OnceBootstrapper so Get (which dials every configured RPC) runs at
	// most once even if several services resolve this dependency.
	return standalone.OnceBootstrapper[evmclient.Client](&dependency{lggr: lggr})
}

// Config is the EVM client configuration. At least one http-url is required; WebSocket URLs
// are optional, and without them the client polls for heads rather than subscribing, which is
// enough for view calls.
type Config struct {
	HTTPURLs  []string `toml:"http-url" usage:"EVM RPC HTTP URL(s); repeat or comma-separate for a multinode pool" validate:"required" example:"['https://rpc.example.com']"`
	WSURLs    []string `toml:"ws-url" usage:"EVM RPC WebSocket URL(s), positionally paired with --evm.http-url; optional" validate:"excluded_without=HTTPURLs"`
	ChainID   string   `toml:"chain-id" usage:"EVM chain ID" validate:"required" example:"'1'"`
	ChainType string   `toml:"chain-type" usage:"EVM chain type (empty for a generic EVM chain)"`

	FinalityTagEnabled bool            `toml:"finality-tag-enabled" usage:"use the finalized block tag instead of a finality depth"`
	FinalityDepth      uint32          `toml:"finality-depth" usage:"finality depth, used when --evm.finality-tag-enabled=false"`
	PollInterval       config.Duration `toml:"poll-interval" usage:"per-node health poll interval"`
}

var defaultConfig = Config{
	FinalityTagEnabled: true,
	FinalityDepth:      defaultFinalityDepth,
	PollInterval:       *config.MustNewDuration(defaultPollInterval),
}

type dependency struct {
	lggr logger.Logger

	client evmclient.Client
	cfg    Config
}

var _ standalone.BootstrapDependency[evmclient.Client] = (*dependency)(nil)

// Namespace groups the EVM settings under evm.* (--evm.http-url, CRE_EVM_HTTP_URL).
func (d *dependency) Namespace() string { return "evm" }

func (d *dependency) AddCommands(cmd *cobra.Command) {
	d.cfg = defaultConfig
	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = d.Namespace()
	if err := flags.RegisterCommandFlags(cmd, &d.cfg, opts); err != nil {
		panic(err)
	}
}

func (d *dependency) Get(ctx context.Context, _ standalone.CommonConfig) (evmclient.Client, error) {
	if len(d.cfg.WSURLs) > 0 && len(d.cfg.WSURLs) != len(d.cfg.HTTPURLs) {
		return nil, fmt.Errorf("--evm.ws-url count (%d) must match --evm.http-url count (%d) when provided", len(d.cfg.WSURLs), len(d.cfg.HTTPURLs))
	}

	chainID, ok := new(big.Int).SetString(d.cfg.ChainID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid --evm.chain-id %q", d.cfg.ChainID)
	}

	nodeCfgs := make([]evmclient.NodeConfig, len(d.cfg.HTTPURLs))
	for i := range d.cfg.HTTPURLs {
		name := fmt.Sprintf("node-%d", i)
		order := int32(1)
		sendOnly := false
		loadBalanced := false
		cfg := evmclient.NodeConfig{
			Name:              &name,
			HTTPURL:           &d.cfg.HTTPURLs[i],
			Order:             &order,
			SendOnly:          &sendOnly,
			IsLoadBalancedRPC: &loadBalanced,
		}
		if len(d.cfg.WSURLs) > 0 {
			cfg.WSURL = &d.cfg.WSURLs[i]
		}
		nodeCfgs[i] = cfg
	}

	selectionMode := defaultSelectionMode
	pollFailureThreshold := defaultPollFailureThreshold
	pollSuccessThreshold := defaultPollSuccessThreshold
	syncThreshold := defaultSyncThreshold
	nodeIsSyncingEnabled := false
	finalityDepth := d.cfg.FinalityDepth
	finalityTagEnabled := d.cfg.FinalityTagEnabled
	safeTagSupported := false
	finalizedBlockOffset := defaultFinalizedBlockOffset
	enforceRepeatableRead := true
	safeDepth := defaultSafeDepth

	chainCfg, nodePool, nodes, err := evmclient.NewClientConfigs(
		&selectionMode,
		defaultLeaseDuration,
		d.cfg.ChainType,
		nodeCfgs,
		&pollFailureThreshold,
		&pollSuccessThreshold,
		d.cfg.PollInterval.Duration(),
		&syncThreshold,
		&nodeIsSyncingEnabled,
		defaultNoNewHeadsThreshold,
		&finalityDepth,
		&finalityTagEnabled,
		&safeTagSupported,
		&finalizedBlockOffset,
		&enforceRepeatableRead,
		defaultDeathDeclarationDelay,
		defaultNoNewFinalizedHeads,
		defaultFinalizedBlockPollInterval,
		defaultNewHeadsPollInterval,
		defaultConfirmationTimeout,
		&safeDepth,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build evm client configs: %w", err)
	}

	// clientErrors is nil: it only feeds ClassifySendError on the transaction send
	// path, and this client is read-only.
	cl, err := evmclient.NewEvmClient(nodePool, chainCfg, nil, d.lggr, chainID, nodes, chaintype.ChainType(d.cfg.ChainType))
	if err != nil {
		return nil, fmt.Errorf("failed to create evm client: %w", err)
	}

	if err := cl.Dial(ctx); err != nil {
		return nil, fmt.Errorf("failed to dial evm client: %w", err)
	}

	d.lggr.Infow("EVM client dialed", "chainID", chainID, "nodes", len(nodes), "selectionMode", selectionMode)
	d.client = cl
	return cl, nil
}

func (d *dependency) Close() {
	if d.client != nil {
		d.client.Close()
	}
}
