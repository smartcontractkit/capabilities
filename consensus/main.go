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
this node's rage identity, which signs on this capability's behalf and serves the
registry saying what OCR configuration this oracle runs under.
--capabilities.proxy-url is where the capabilities this binary hosts are announced.

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
				DonID: deps.CapabilityDonID,
				// The configuration comes from the registry, whichever form resolved it: the node's
				// for a configured run, and one computed over the run's own instances for an embedded
				// one. Either way this reads it off the same field and cannot tell them apart.
				Registry:        deps.OCRConfigRegistry,
				Endpoints:       factories.OCR2Endpoint,
				Offchain:        factories.Offchain,
				Onchain:         factories.Onchain,
				TransmitAccount: factories.TransmitAccount,
				Bootstrappers:   factories.Bootstrappers,
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
//
// The DON's bootstrap peers are not here: where a peer can be reached is a property of the network
// this process delegates to, so it is configured with that network - see ocr.ProxyConfig.
type Config struct {
	action.ConsensusCapabilityConfig `toml:",inline"`
}

var defaultConfig = Config{}
