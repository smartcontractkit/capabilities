package standalone

//go:generate go run ./gen

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/hashicorp/go-plugin"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// StandaloneConfig holds the process-wide dependencies the Bootstrapper provides
// to the service factory passed to Run.
type StandaloneConfig struct {
	// Logger is hclog-compatible like a LOOP plugin's: JSON on stderr with
	// @level/@message/@timestamp keys, so a go-plugin host (e.g. the core node)
	// can parse and re-level the entries when this process runs under one,
	// while remaining plain JSON logs when run standalone.
	Logger logger.SugaredLogger
}

type Bootstrapper struct {
	root           *cobra.Command
	config         *StandaloneConfig
	commonConfig   CommonConfig
	beholderClient *beholder.Client // nil unless CL_TELEMETRY_ENDPOINT is configured
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

	bs := &Bootstrapper{root: root, config: &StandaloneConfig{Logger: slggr}, beholderClient: beholderClient}
	root.PersistentFlags().BoolVar(&bs.commonConfig.Fake, "fake", false, "use fake dependencies instead of real ones")
	return bs
}

// close flushes telemetry and logs: the counterpart to the setup in NewBootstrapper.
func (b *Bootstrapper) close() {
	if b.beholderClient != nil {
		b.config.Logger.ErrorIfFn(b.beholderClient.Close, "Failed to close beholder client")
	}
	_ = b.config.Logger.Sync()
}

// Config returns the StandaloneConfig that is also passed to the service
// factory, for dependencies that need e.g. the Logger before Run is called.
func (b *Bootstrapper) Config() *StandaloneConfig { return b.config }

// run composes the services returned by factory into a single supervising
// service via services.Engine sub-services, so their health is aggregated the
// same way the rest of the stack does it (services.Config.NewSubServices +
// HealthReport). It starts them, then blocks until an interrupt, then closes.
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

// underPluginHost reports whether this process was launched by a go-plugin host,
// detected via the empty plugin's handshake magic cookie. go-plugin's Serve
// refuses to run (and exits) when this is absent, so we only serve the plugin in
// that case and otherwise run standalone.
func underPluginHost() bool {
	h := loop.EmptyHandshakeConfig()
	return os.Getenv(h.MagicCookieKey) == h.MagicCookieValue
}

type CommonConfig struct {
	Fake bool
}

type Dependency[T any] interface {
	Get(ctx context.Context) (T, error)
}

type BootstrapCommand interface {
	AddCommands(*cobra.Command)
}

type BootstrapDependency[T any] interface {
	Get(ctx context.Context, c CommonConfig) (T, error)
	BootstrapCommand
}

type dependency[T any] struct {
	bs *Bootstrapper
	bd BootstrapDependency[T]
}

func (d dependency[T]) Get(ctx context.Context) (T, error) {
	return d.bd.Get(ctx, d.bs.commonConfig)
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
