// Command consensus runs the consensus capability as its own binary.
//
// It hosts no node of its own: rage networking, the keys it signs with and the
// capabilities registry all come from the crecore process it is pointed at, and
// what is left here is the capability - the OCR plugin that reaches consensus
// over a workflow's observations, and the server that makes it callable.
package main

import (
	"context"
	"log"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/consensus/action"
	"github.com/smartcontractkit/capabilities/consensus/metrics"
	"github.com/smartcontractkit/capabilities/consensus/protos"
	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/capability"
	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"
	"github.com/smartcontractkit/capabilities/libs/standalone/ocr"
)

// responseCacheExpiry is how long a response is kept after consensus is reached,
// so that a request arriving late still gets the answer rather than starting the
// round again.
const responseCacheExpiry = time.Minute

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := defaultConfig

	root := &cobra.Command{
		Use:   "consensus",
		Short: "The CRE consensus capability",
		Long: `Runs the consensus capability, which reaches OCR consensus over the
observations a workflow's nodes make and returns the agreed result.

It holds no keys and no peer: --ocr.proxy-address is the crecore process hosting
this node's rage identity, which also signs on this capability's behalf, and
--capabilities.proxy-url is the registry that says which DON this is and what
OCR configuration it runs under.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file;
run "docs" to write the full reference to docs/CONFIG.md.`,
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = "consensus"
	if err := flags.RegisterCommandFlags(root, &cfg, opts); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root, standalone.WithOtelViews(metrics.MetricViews()))
	lggr := bootstrapper.Logger()

	// The proxy form, not the host one: this binary drives an oracle, it does not run a peer.
	ocrDep := ocr.Proxy(lggr.Named("OCR"))
	capDep := capability.Dependency(lggr.Named("Capabilities"), standalonegrpc.FactoryDependency(lggr.Named("CapabilityAPI")))

	return standalone.Run2(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		factories *ocr.OCRFactories,
		deps capability.Dependencies,
	) []services.Service {
		lggr := scfg.Logger.Named("consensus")

		capabilityImpl, err := action.NewConsensusCapability(lggr, clockwork.NewRealClock(), responseCacheExpiry,
			cfg.ConsensusCapabilityConfig,
			action.Dependencies{
				DonID:           deps.CapabilityDonID,
				Registry:        deps.OCRConfigRegistry(),
				Endpoints:       factories.OCR2Endpoint,
				Offchain:        factories.Offchain,
				Onchain:         factories.Onchain,
				TransmitAccount: factories.TransmitAccount,
				Bootstrappers:   cfg.Bootstrappers.ToBootstrapperLocators(),
				LimitsFactory:   deps.LimitsFactory,
				Metrics:         scfg.MetricsRegisterer,
			})
		if err != nil {
			lggr.Fatalw("Failed to create ConsensusCapability", "error", err)
		}

		// Run supervises the capability and makes it reachable: registered,
		// served, and announced to the node's registry.
		svcs, err := capability.Run(deps, *scfg, protos.NewConsensusServer(capabilityImpl))
		if err != nil {
			lggr.Fatalw("Failed to host ConsensusCapability", "error", err)
		}
		return svcs
	}, ocrDep, capDep)
}

// Config is what this binary needs that its host cannot tell it.
type Config struct {
	action.ConsensusCapabilityConfig `toml:",inline"`

	Bootstrappers config.BootstrapperLocators `usage:"peerID@host:port of the DON's bootstrap peers" example:"['12D3KooWFirst@127.0.0.1:6690']"`
}

var defaultConfig = Config{}
