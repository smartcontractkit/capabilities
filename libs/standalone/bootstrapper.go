package standalone

//go:generate go run ./gen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/hashicorp/go-plugin"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/otelhealth"
	"github.com/smartcontractkit/chainlink-common/pkg/services/promhealth"

	"github.com/grafana/pyroscope-go"
)

// StandaloneConfig holds the process-wide dependencies the Bootstrapper provides
// to the service factory passed to Run.
type StandaloneConfig struct {
	// Logger is hclog-compatible like a LOOP plugin's: JSON on stderr with
	// @level/@message/@timestamp keys, so a go-plugin host (e.g. the core node)
	// can parse and re-level the entries when this process runs under one,
	// while remaining plain JSON logs when run standalone.
	Logger logger.SugaredLogger

	// BeholderClient is the process-wide telemetry client, nil unless
	// CL_TELEMETRY_ENDPOINT is configured. Exposed so a factory can build its
	// own otel instruments (meters, tracers) consistent with the rest of the
	// process, the same way a LOOP plugin would reach beholder.GetClient().
	BeholderClient *beholder.Client
}

type Bootstrapper struct {
	root         *cobra.Command
	config       *StandaloneConfig
	commonConfig CommonConfig
	profiler     *pyroscope.Profiler // nil unless CL_PYROSCOPE_SERVER_ADDRESS is configured

	closersMu sync.Mutex
	closers   []io.Closer // resolved dependency values that implement io.Closer
}

// NewBootstrapper creates a new Bootstrapper using the cobra command as its root.
// Note that the RunE on the cobra command will be overwritten when Run is called, and the cobra command is provided only for the remaining fields.
//
// It creates the hclog-compatible logger and, when CL_TELEMETRY_* env vars are
// configured, starts beholder telemetry with any otel views from opts; it exits
// on failure. The logger runs and supervises the services (health, lifecycle
// logging) and is available via Config for use before Run.
func NewBootstrapper(root *cobra.Command, opts ...Option) *Bootstrapper {
	var s settings
	for _, opt := range opts {
		opt(&s)
	}

	lggr, err := newLogger()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Failed to create logger: %s\n", err)
		os.Exit(1)
	}
	slggr := logger.Sugared(logger.Named(lggr, root.Name()))

	beholderClient, err := startTelemetry(context.Background(), s.otelViews)
	if err != nil {
		slggr.Fatalf("Failed to start telemetry: %s", err)
	}

	profiler, err := startProfiler(root.Name())
	if err != nil {
		slggr.Fatalf("Failed to start profiler: %s", err)
	}

	bs := &Bootstrapper{
		root:     root,
		config:   &StandaloneConfig{Logger: slggr, BeholderClient: beholderClient},
		profiler: profiler,
	}
	// Registered through pkg/config/flags like every other config struct, so --fake is
	// documented alongside the settings it interacts with rather than being invisible to the
	// generated docs. It stays at the top level: it is process-wide, not one dependency's.
	if err := flags.RegisterCommandFlags(root, &bs.commonConfig, flags.DefaultTOMLOptions("CRE", "CL")); err != nil {
		slggr.Fatalf("Failed to register common flags: %s", err)
	}
	return bs
}

// close closes resolved dependencies (in reverse resolution order), stops
// profiling, flushes telemetry, and syncs logs: the counterpart to the setup
// in NewBootstrapper and to dependency resolution during run.
func (b *Bootstrapper) close() {
	b.closersMu.Lock()
	closers := b.closers
	b.closersMu.Unlock()

	for i := len(closers) - 1; i >= 0; i-- {
		b.config.Logger.ErrorIfFn(closers[i].Close, "Failed to close dependency")
	}

	if b.profiler != nil {
		b.config.Logger.ErrorIfFn(b.profiler.Stop, "Failed to stop pyroscope profiler")
	}
	if b.config.BeholderClient != nil {
		b.config.Logger.ErrorIfFn(b.config.BeholderClient.Close, "Failed to close beholder client")
	}
	_ = b.config.Logger.Sync()
}

// registerCloser records v for closing on shutdown if it implements
// io.Closer. Safe to call concurrently.
func (b *Bootstrapper) registerCloser(v any) {
	c, ok := v.(io.Closer)
	if !ok {
		return
	}
	b.closersMu.Lock()
	b.closers = append(b.closers, c)
	b.closersMu.Unlock()
}

// Logger returns the logger instance. It is safe to call before running the binary
func (b *Bootstrapper) Logger() logger.SugaredLogger { return b.config.Logger }

// run composes the services returned by factory into a single supervising
// service via services.Engine sub-services, so their health is aggregated the
// same way the rest of the stack does it (services.Config.NewSubServices +
// HealthReport). It starts them along with a health checker (registered
// against the aggregated root service) and, if CL_PROMETHEUS_PORT is set, a
// web server on that port serving /metrics, /debug/pprof, and /healthz +
// /readyz (backed by the health checker), then blocks until an interrupt,
// then closes everything in reverse.
func (b *Bootstrapper) run(factory func(ctx context.Context) []services.Service) error {
	defer b.close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b.root.RunE = func(cmd *cobra.Command, args []string) error {
		svcs := factory(ctx)
		root, _ := services.Config{
			Name:           "Bootstrap",
			NewSubServices: func(logger.Logger) []services.Service { return svcs },
		}.NewServiceEngine(b.config.Logger)

		if err := root.Start(ctx); err != nil {
			stop()
			return err
		}
		defer func() { _ = root.Close() }()

		checker, err := b.startHealthChecker(root)
		if err != nil {
			stop()
			return err
		}
		defer func() { b.config.Logger.ErrorIfFn(checker.Close, "Failed to close health checker") }()

		if port, ok, err := prometheusPort(); err != nil {
			stop()
			return err
		} else if ok {
			http.HandleFunc("/healthz", healthHandler(checker.IsHealthy))
			http.HandleFunc("/readyz", healthHandler(checker.IsReady))

			web := loop.WebServerOpts{}.New(b.config.Logger, port)
			if err := web.Start(ctx); err != nil {
				stop()
				return fmt.Errorf("failed to start prometheus web server: %w", err)
			}
			defer func() { b.config.Logger.ErrorIfFn(web.Close, "Failed to close prometheus web server") }()
		}

		if underPluginHost() {
			// Launched by a go-plugin host (e.g. the core node): expose the empty
			// LOOP so the host can supervise this process over gRPC (handshake +
			// go-plugin's liveness health check). The started services run in
			// this process, so that liveness reflects them. Blocks until the host
			// shuts us down.
			plugin.Serve(&plugin.ServeConfig{
				HandshakeConfig: loop.EmptyHandshakeConfig(),
				Plugins:         map[string]plugin.Plugin{loop.PluginEmptyName: &loop.EmptyLoop{}},
				GRPCServer:      plugin.DefaultGRPCServer,
			})
			return nil
		}

		// Standalone: block until interrupted, then close.
		<-ctx.Done()
		return nil
	}

	return b.root.Execute()
}

// healthHandler adapts a services.HealthChecker.IsHealthy/IsReady-shaped func into
// an HTTP handler: 200 with each check's status when ok, 503 and the failing
// checks' errors otherwise.
func healthHandler(check func() (bool, map[string]error)) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		ok, errs := check()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		for name, err := range errs {
			if err != nil {
				fmt.Fprintf(w, "%s: %s\n", name, err)
			}
		}
		if ok {
			fmt.Fprintln(w, "ok")
		}
	}
}

// startHealthChecker builds a services.HealthChecker that mirrors reporter (usually
// the aggregated root service) as prometheus metrics ("health", "uptime_seconds",
// "version") and, when telemetry is configured, as otel metrics through the same
// beholder client/meter the rest of the process uses.
func (b *Bootstrapper) startHealthChecker(reporter services.HealthReporter) (*services.HealthChecker, error) {
	cfg := promhealth.ConfigureHooks(services.HealthCheckerConfig{})
	if bc := b.config.BeholderClient; bc != nil {
		var err error
		cfg, err = otelhealth.ConfigureHooks(cfg, bc.Meter)
		if err != nil {
			return nil, fmt.Errorf("failed to configure health checker otel hooks: %w", err)
		}
	}

	checker := cfg.New()
	if err := checker.Start(); err != nil {
		return nil, fmt.Errorf("failed to start health checker: %w", err)
	}
	if err := checker.Register(reporter); err != nil {
		return nil, fmt.Errorf("failed to register health checker reporter: %w", err)
	}
	return checker, nil
}

// underPluginHost reports whether this process was launched by a go-plugin host,
// detected via the empty plugin's handshake magic cookie. go-plugin's Serve
// refuses to run (and exits) when this is absent, so we only serve the plugin in
// that case and otherwise run standalone.
func underPluginHost() bool {
	h := loop.EmptyHandshakeConfig()
	return os.Getenv(h.MagicCookieKey) == h.MagicCookieValue
}

type CommonConfig struct {
	// Kept out of the example config: the example shows a real run, and --fake selects a
	// different set of dependencies than the ones it illustrates.
	Fake bool `toml:"fake" usage:"use fake dependencies instead of real ones" flagdocs:"noexample"`
}

type Dependency[T any] interface {
	Get(ctx context.Context) (T, error)
}

type BootstrapCommand interface {
	AddCommands(*cobra.Command)

	// Namespace roots this dependency's configuration, so its settings group together and
	// same-named settings from different dependencies don't collide - "database" gives
	// --database.url, the key database.url and the env var CRE_DATABASE_URL. Return "" to
	// keep the settings at the top level.
	Namespace() string
}

type BootstrapDependency[T any] interface {
	Get(ctx context.Context, c CommonConfig) (T, error)
	BootstrapCommand
}

type dependency[T any] struct {
	bs *Bootstrapper
	bd BootstrapDependency[T]

	registerOnce sync.Once // guards registering the resolved value with bs, since Get may be called more than once
}

func (d *dependency[T]) Get(ctx context.Context) (T, error) {
	v, err := d.bd.Get(ctx, d.bs.commonConfig)
	if err == nil {
		d.registerOnce.Do(func() { d.bs.registerCloser(v) })
	}
	return v, err
}

// OnceBootstrapper wraps a BootstrapDependency so that Get is evaluated at most
// once: the first call resolves the dependency and caches its (value, error),
// and every subsequent call returns that same result without re-running Get
// (the ctx and CommonConfig of later calls are ignored). AddCommands is
// delegated unchanged.
//
// BootstrapDependency implementations are shared and may have Get called more
// than once — e.g. one dependency resolving another, or the same dependency
// feeding several services — so a New function should wrap its dependency with
// OnceBootstrapper before returning it, making repeated Get calls safe and
// side-effect-free.
func OnceBootstrapper[T any](bd BootstrapDependency[T]) BootstrapDependency[T] {
	return &onceBootstrapper[T]{bd: bd}
}

type onceBootstrapper[T any] struct {
	bd   BootstrapDependency[T]
	once sync.Once
	val  T
	err  error
}

func (o *onceBootstrapper[T]) Get(ctx context.Context, c CommonConfig) (T, error) {
	o.once.Do(func() {
		o.val, o.err = o.bd.Get(ctx, c)
	})
	return o.val, o.err
}

func (o *onceBootstrapper[T]) AddCommands(cmd *cobra.Command) {
	o.bd.AddCommands(cmd)
}

func (o *onceBootstrapper[T]) Namespace() string { return o.bd.Namespace() }
