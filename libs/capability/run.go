package capability

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/hashicorp/go-plugin"
	"github.com/samber/do/v2"
	"github.com/spf13/cobra"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/x/config/cli"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"
)

// Constructor builds the capability from the injector of the instance it is built for, which
// provides everything registered with Provide and ProvideConfig - for embed, ProvideForEmbed and
// ProvideConfigForEmbed - along with what the run provides: the logger, the registry, the limits
// factory, the config binder, and the capability's own config, as the T its ConfigProvider
// declared.
type Constructor func(do.Injector) (Capability, error)

// ConfigProvider is the capability's own config: its default, which flags, env vars and config
// files decode over, and how to register it - a profile, say. Run registers it with ProvideConfig
// under the binary's name.
type ConfigProvider[T any] struct {
	defaultConfig T
	opts          []cli.RegisterOption[T]
}

// NewConfigProvider declares a T, defaulting to defaultConfig, as the capability's own config.
// Run binds its fields as flags on the root command, namespaced by the binary's name - cron's are
// --cron.fastest-schedule-interval-seconds, CRE_CRON_FASTEST_SCHEDULE_INTERVAL_SECONDS, or the
// same key in a cron section of the config file - and provides the decoded T to the capability's
// container. A capability with no config passes NewConfigProvider(struct{}{}).
func NewConfigProvider[T any](defaultConfig T, opts ...cli.RegisterOption[T]) ConfigProvider[T] {
	return ConfigProvider[T]{defaultConfig: defaultConfig, opts: opts}
}

// Run builds and runs the capability ctor builds, once per instance, and does not return until ctx
// is cancelled or the process is signalled.
//
//	func main() {
//		capability.Run(ctx, trigger.NewCron, capability.NewConfigProvider(trigger.DefaultConfig),
//			capability.WithOtelViews(trigger.MetricViews()...))
//	}
func Run[T any](ctx context.Context, ctor Constructor, config ConfigProvider[T], opts ...Option) {
	lggr, err := NewLogger()
	if err != nil {
		// lggr is nil when building it failed.
		log.Fatal(err)
	}

	if err = RunErr(ctx, lggr, ctor, config, opts...); err != nil {
		lggr.Fatal(err)
	}
}

// RunErr is Run, returning the error rather than exiting on it.
//
// A run blocks until it is told to stop, which is either ctx being cancelled or this process being
// signalled - see below.
func RunErr[T any](ctx context.Context, lggr logger.Logger, ctor Constructor, config ConfigProvider[T], opts ...Option) error {
	if lggr == nil {
		return errors.New("must provide a logger")
	}
	if ctor == nil {
		return errors.New("must provide a capability constructor")
	}

	// TODO: Run should accept some kind of Info struct
	root := &cobra.Command{
		Use:   filepath.Base(os.Args[0]),
		Short: "A CRE capability",
	}

	lggr = logger.Named(lggr, root.Name())

	b, err := cli.New(cli.Options{Markup: tomlmarkup.New(), Prefixes: []string{"CRE", "CL"}})
	if err != nil {
		return err
	}

	// The run's own settings, then the capability's own config under the binary's name - after
	// every dependency's init, so a dependency registering one of these first is a duplicate.
	if err = registerAll(root.Name(), config); err != nil {
		return err
	}

	// serve starts total instances from r and blocks until ctx is done. Only run hands the process
	// to a go-plugin host: a host supervises one plugin, and an embed run is not one.
	serve := func(cmd *cobra.Command, r *registrations, total int, pluginHost bool) error {
		defer func() { _ = lggr.Sync() }()

		stop, err := start(cmd.Context(), lggr, cmd.Root().Name(), options(opts), r, b, total, ctor)
		if err != nil {
			return err
		}
		defer stop()

		if pluginHost && underPluginHost() {
			lggr.Info("Serving the empty LOOP: this process is supervised by a go-plugin host")
			plugin.Serve(&plugin.ServeConfig{
				HandshakeConfig: emptyHandshakeConfig(),
				Plugins:         map[string]plugin.Plugin{emptyPluginName: &emptyLoop{}},
				GRPCServer:      plugin.DefaultGRPCServer,
			})
			return nil
		}

		<-cmd.Context().Done()
		lggr.Info("Shutting down")
		return nil
	}

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run the capability",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return serve(cmd, runRegistrations, 1, true)
		},
	}

	embedCmd := &cobra.Command{
		Use:   "embed",
		Short: "Run several instances of the capability in this process",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := embedRegistrations.configs[""].value.(*embedConfig)
			return serve(cmd, embedRegistrations, cfg.Instances, false)
		},
	}

	root.AddCommand(runCmd, embedCmd, docsCommand())

	// Each command binds what was registered for it. They are siblings, so a config registered for
	// both is bound on both without clashing.
	if err = runRegistrations.bind(b, runCmd); err != nil {
		return err
	}
	if err = embedRegistrations.bind(b, embedCmd); err != nil {
		return err
	}

	// Wire up SIGTERM + SIGINT to the context
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return root.ExecuteContext(ctx)
}

// registerAll registers the run's own settings and the capability's config, recovering the panic a
// duplicate registration raises into an error - here it is the operator's binary, not init, that
// would be crashing.
func registerAll[T any](name string, config ConfigProvider[T]) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("failed to register the run's settings: %v", r)
		}
	}()
	registerSettings()
	ProvideConfig(name, config.defaultConfig, config.opts...)
	return nil
}

// runOptions is what a binary passes Run that is not a setting an operator can change.
type runOptions struct {
	otelViews []sdkmetric.View
}

type Option func(*runOptions)

func options(opts []Option) runOptions {
	var o runOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// WithOtelViews sets otel metric views - histogram bucket boundaries, typically - on the beholder
// client this process reports through.
//
//	capability.Run(ctx, trigger.NewCron, capability.NewConfigProvider(trigger.DefaultConfig),
//		capability.WithOtelViews(trigger.MetricViews()...))
func WithOtelViews(views ...sdkmetric.View) Option {
	return func(o *runOptions) { o.otelViews = append(o.otelViews, views...) }
}
