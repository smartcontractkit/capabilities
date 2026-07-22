package standalone

//go:generate go run ./gen

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/hashicorp/go-plugin"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type Bootstrapper struct {
	root         *cobra.Command
	lggr         logger.Logger
	commonConfig CommonConfig
}

// NewBootstrapper creates a new Bootstrapper using the cobra command as its root.
// Note that the RunE on the cobra command will be overwritten when Run is called, and the cobra command is provided only for the remaining fields.
// lggr is used to run and supervise the services (health, lifecycle logging).
func NewBootstrapper(root *cobra.Command, lggr logger.Logger) *Bootstrapper {
	bs := &Bootstrapper{root: root, lggr: lggr}
	root.PersistentFlags().BoolVar(&bs.commonConfig.Fake, "fake", false, "use fake dependencies instead of real ones")
	return bs
}

// run composes the services returned by factory into a single supervising
// service via services.Engine sub-services, so their health is aggregated the
// same way the rest of the stack does it (services.Config.NewSubServices +
// HealthReport). It starts them, then blocks until an interrupt, then closes.
func (b *Bootstrapper) run(factory func(ctx context.Context) []services.Service) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b.root.RunE = func(cmd *cobra.Command, args []string) error {
		svcs := factory(ctx)
		root, _ := services.Config{
			Name:           "Bootstrap",
			NewSubServices: func(logger.Logger) []services.Service { return svcs },
		}.NewServiceEngine(b.lggr)

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
