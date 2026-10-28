package capability

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/do/v2"
	"github.com/spf13/cobra"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

// Run builds and runs the capability constructor makes, and does not return until the process is
// signalled. To stop it with a context, use RunErr.
//
//	func main() { capability.Run("cron", trigger.NewCron) }
func Run[T Capability](appName string, constructor func(do.Injector) (T, error), opts ...Option) {
	if err := RunErr(context.Background(), appName, constructor, opts...); err != nil {
		log.Fatal(err)
	}
}

// RunErr is Run, returning the error rather than exiting on it.
//
// A run blocks until it is told to stop, which is either ctx being cancelled or this process being
// signalled - see below.
func RunErr[T Capability](ctx context.Context, appName string, ctor func(do.Injector) (T, error), opts ...Option) error {
	if appName == "" {
		return errors.New("must provide an app name")
	}
	if ctor == nil {
		return errors.New("must provide a capability constructor")
	}
	constructor := func(i do.Injector) (Capability, error) { return ctor(i) }

	// TODO: Run should accept some kind of Info struct
	root := &cobra.Command{
		Use:   filepath.Base(os.Args[0]),
		Short: "A CRE capability",
	}

	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"compat"}
	}
	root.SetArgs(args)

	cfg := defaultConfig(appName)
	cfg.runInjector = RunInjector.Clone()
	cfg.embedInjector = EmbedInjector.Clone()
	for _, opt := range opts {
		err := opt(cfg)
		if err != nil {
			return err
		}
	}

	lggr := cfg.lggr
	if lggr == nil {
		l, err := loop.NewLogger()
		if err != nil {
			return fmt.Errorf("failed to create logger: %w", err)
		}

		lggr = l
	}
	lggr = logger.Named(lggr, appName)

	b, err := cfg.bind(root)
	if err != nil {
		return err
	}

	compatCmd := &cobra.Command{
		Use:    "compat",
		Short:  "Run the capability as a LOOP of the core node",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !underPluginHost() {
				return errors.New("compatibility mode must be started by a go-plugin host, such as the core node. To run the capability alone, use the run command")
			}
			if cfg.observability.PrometheusPort == 0 {
				return errors.New("compatibility mode needs CL_PROMETHEUS_PORT")
			}
			serve := func(context.Context) { servePlugin(lggr) }
			if cfg.serve != nil {
				serve = cfg.serve
			}
			return newRunner(lggr, appName, cfg, constructor, cfg.runInjector, 1, cfg.capabilities, HTTPConfig{Port: cfg.observability.PrometheusPort}, serve).run(cmd.Context())
		},
	}

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run the capability",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return newRunner(lggr, appName, cfg, constructor, cfg.runInjector, 1, cfg.capabilities, cfg.observability.http, nil).run(cmd.Context())
		},
	}

	embedCmd := &cobra.Command{
		Use:   "embed <instances>",
		Short: "Run instances of the capability in this process, with no node behind them",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			total, err := strconv.Atoi(args[0])
			if err != nil || total < 1 {
				return fmt.Errorf("instances must be a positive integer, got %q", args[0])
			}
			capCfg := cfg.capabilities
			capCfg.ProxyURL = ""
			return newRunner(lggr, appName, cfg, constructor, cfg.embedInjector, total, capCfg, cfg.observability.http, nil).run(cmd.Context())
		},
	}

	if err := errors.Join(
		cfg.runInjector.RegisterConfig(compatCmd, b),
		cfg.runInjector.RegisterConfig(runCmd, b),
		cfg.embedInjector.RegisterConfig(embedCmd, b),
	); err != nil {
		return err
	}
	root.AddCommand(compatCmd, runCmd, embedCmd)

	// Wire up SIGTERM + SIGINT to the context
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return root.ExecuteContext(ctx)
}

type Option func(*config) error

// withLogger gives a run the logger of a test. Other runs make their own logger.
func withLogger(lggr logger.Logger) Option {
	return func(c *config) error {
		c.lggr = lggr
		return nil
	}
}

// withServe replaces plugin.Serve in compatibility mode. plugin.Serve takes over the stdio of the
// process, so tests can't call it.
func withServe(serve func(context.Context)) Option {
	return func(c *config) error {
		c.serve = serve
		return nil
	}
}

// withRegisterer gives a run its own metrics registry. Tests start many runs in one process.
// The default registry panics if you register the same metrics two times.
func withRegisterer(r prometheus.Registerer) Option {
	return func(c *config) error {
		c.grpc.Registerer = r
		return nil
	}
}

// WithOtelViews sets otel metric views - histogram bucket boundaries, typically - on the beholder
// client this process reports through.
//
//	capability.Run("cron", trigger.NewCron, capability.WithOtelViews(trigger.MetricViews()...))
func WithOtelViews(views ...sdkmetric.View) Option {
	return func(c *config) error {
		c.observability.otelViews = append(c.observability.otelViews, views...)
		return nil
	}
}

func WithConfig[T any]() Option {
	return func(c *config) error {
		return MultiInjector{c.runInjector, c.embedInjector}.ProvideConfig(c.appName, new(T))
	}
}
