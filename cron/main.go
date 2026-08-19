// Command cron runs the cron trigger capability as its own binary.
//
// It hosts no node of its own: the capabilities registry it announces itself to,
// and the settings its limits resolve against, come from the crecore process it
// is pointed at, and what is left here is the capability - the scheduler that
// fires a workflow's trigger on time, and the server that makes it registerable.
package main

import (
	"context"
	"log"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/services"

	"github.com/smartcontractkit/capabilities/cron/protos"
	"github.com/smartcontractkit/capabilities/cron/trigger"
	"github.com/smartcontractkit/capabilities/libs/standalone"
	"github.com/smartcontractkit/capabilities/libs/standalone/capability"
	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg := defaultConfig

	root := &cobra.Command{
		Use:   "cron",
		Short: "The CRE cron trigger capability",
		Long: `Runs the cron trigger capability, which fires a workflow's trigger on the
schedule that workflow registered.

It holds no keys and no peer: --capabilities.proxy-url is the node's registry,
which this binary announces the capability to and reads its settings from.

Settings can come from flags, from CRE_/CL_ env vars, or from a --config file;
run "docs" to write the full reference to docs/CONFIG.md.`,
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = "cron"
	if err := flags.RegisterCommandFlags(root, &cfg, opts); err != nil {
		return err
	}

	bootstrapper := standalone.NewBootstrapper(root, standalone.WithOtelViews(trigger.MetricViews()))
	lggr := bootstrapper.Logger()

	capDep := capability.Dependency(lggr.Named("Capabilities"), standalonegrpc.FactoryDependency(lggr.Named("CapabilityAPI")))

	return standalone.Run1(bootstrapper, func(
		ctx context.Context,
		scfg *standalone.StandaloneConfig,
		deps capability.Dependencies,
	) []services.Service {
		lggr := scfg.Logger.Named("cron")

		// The real clock: nil is the constructor's "a test drives this one".
		triggerService, err := trigger.NewTriggerService(lggr, nil, cfg.Config, trigger.Dependencies{
			LimitsFactory: deps.LimitsFactory,
		})
		if err != nil {
			lggr.Fatalw("Failed to create cron trigger service", "error", err)
		}

		// Run supervises the capability and makes it reachable: registered,
		// served, and announced to the node's registry.
		svcs, err := capability.Run(deps, *scfg, protos.NewCronServer(triggerService))
		if err != nil {
			lggr.Fatalw("Failed to host CronCapability", "error", err)
		}
		return svcs
	}, capDep)
}

// Config is what this binary needs that its host cannot tell it.
type Config struct {
	trigger.Config `toml:",inline"`
}

var defaultConfig = Config{}
