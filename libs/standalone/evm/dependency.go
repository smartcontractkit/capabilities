// Package evm provides a standalone.BootstrapDependency that supplies an EVM RPC
// client to a standalone binary.
//
// The client is chainlink-evm's multinode-backed client.Client, not a bare geth
// *ethclient.Client. That is deliberate: multinode is where the RPC reliability
// behaviour lives (per-node health polling, sync-threshold detection, dead-node
// declaration, primary selection, load-balanced RPC support). A standalone process
// reading a contract needs exactly those properties and should not reimplement
// them.
//
// What this does not bring along is the relayer / ContractReader stack. Callers
// get a client.Client and make bind.ContractCaller view calls against generated
// gethwrappers directly, which is a far shorter path to follow than
// relayer -> ContractReader -> codec -> ReadIdentifier.
package evm

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/spf13/cobra"

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
// At least one --evm-http-url is required. WebSocket URLs are optional; without
// them the client polls for heads rather than subscribing, which is enough for
// view calls.
func Dependency(lggr logger.Logger) standalone.BootstrapDependency[evmclient.Client] {
	// Wrap in OnceBootstrapper so Get (which dials every configured RPC) runs at
	// most once even if several services resolve this dependency.
	return standalone.OnceBootstrapper[evmclient.Client](&dependency{lggr: lggr})
}

type dependency struct {
	lggr logger.Logger

	client evmclient.Client

	httpURLs           []string
	wsURLs             []string
	chainID            string
	chainType          string
	finalityTagEnabled bool
	finalityDepth      uint32
	pollInterval       time.Duration
}

var _ standalone.BootstrapDependency[evmclient.Client] = (*dependency)(nil)

func (d *dependency) AddCommands(cmd *cobra.Command) {
	f := cmd.PersistentFlags()
	f.StringSliceVar(&d.httpURLs, "evm-http-url", nil, "EVM RPC HTTP URL(s); repeat or comma-separate for a multinode pool")
	f.StringSliceVar(&d.wsURLs, "evm-ws-url", nil, "EVM RPC WebSocket URL(s), positionally paired with --evm-http-url; optional")
	f.StringVar(&d.chainID, "evm-chain-id", "", "EVM chain ID")
	f.StringVar(&d.chainType, "evm-chain-type", "", "EVM chain type (empty for a generic EVM chain)")
	f.BoolVar(&d.finalityTagEnabled, "evm-finality-tag-enabled", true, "use the finalized block tag instead of a finality depth")
	f.Uint32Var(&d.finalityDepth, "evm-finality-depth", defaultFinalityDepth, "finality depth, used when --evm-finality-tag-enabled=false")
	f.DurationVar(&d.pollInterval, "evm-poll-interval", defaultPollInterval, "per-node health poll interval")

	standalone.BindWithEnvVar(f.Lookup("evm-http-url"))
	standalone.BindWithEnvVar(f.Lookup("evm-ws-url"))
	standalone.BindWithEnvVar(f.Lookup("evm-chain-id"))
}

func (d *dependency) Get(ctx context.Context, _ standalone.CommonConfig) (evmclient.Client, error) {
	if len(d.httpURLs) == 0 {
		return nil, errors.New("at least one --evm-http-url is required")
	}
	if len(d.wsURLs) > 0 && len(d.wsURLs) != len(d.httpURLs) {
		return nil, fmt.Errorf("--evm-ws-url count (%d) must match --evm-http-url count (%d) when provided", len(d.wsURLs), len(d.httpURLs))
	}

	chainID, ok := new(big.Int).SetString(d.chainID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid --evm-chain-id %q", d.chainID)
	}

	nodeCfgs := make([]evmclient.NodeConfig, len(d.httpURLs))
	for i := range d.httpURLs {
		name := fmt.Sprintf("node-%d", i)
		order := int32(1)
		sendOnly := false
		loadBalanced := false
		cfg := evmclient.NodeConfig{
			Name:              &name,
			HTTPURL:           &d.httpURLs[i],
			Order:             &order,
			SendOnly:          &sendOnly,
			IsLoadBalancedRPC: &loadBalanced,
		}
		if len(d.wsURLs) > 0 {
			cfg.WSURL = &d.wsURLs[i]
		}
		nodeCfgs[i] = cfg
	}

	selectionMode := defaultSelectionMode
	pollFailureThreshold := defaultPollFailureThreshold
	pollSuccessThreshold := defaultPollSuccessThreshold
	syncThreshold := defaultSyncThreshold
	nodeIsSyncingEnabled := false
	finalityDepth := d.finalityDepth
	finalityTagEnabled := d.finalityTagEnabled
	safeTagSupported := false
	finalizedBlockOffset := defaultFinalizedBlockOffset
	enforceRepeatableRead := true
	safeDepth := defaultSafeDepth

	chainCfg, nodePool, nodes, err := evmclient.NewClientConfigs(
		&selectionMode,
		defaultLeaseDuration,
		d.chainType,
		nodeCfgs,
		&pollFailureThreshold,
		&pollSuccessThreshold,
		d.pollInterval,
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
	cl, err := evmclient.NewEvmClient(nodePool, chainCfg, nil, d.lggr, chainID, nodes, chaintype.ChainType(d.chainType))
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
