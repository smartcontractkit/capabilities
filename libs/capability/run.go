package capability

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/samber/do/v2"
	"github.com/spf13/cobra"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Run builds and runs the capability constructor makes, and does not return until ctx is cancelled or the
// process is signalled.
//
//	func main() { capability.Run(context.Background(), trigger.NewCron) }
func Run(ctx context.Context, constructor func(do.Injector) (Capability, error), opts ...Option) {
	lggr, err := NewLogger()
	if err != nil {
		log.Fatal(err)
	}

	if err := RunErr(ctx, lggr, constructor, opts...); err != nil {
		lggr.Fatal(err)
	}
}

// RunErr is Run, returning the error rather than exiting on it.
//
// A run blocks until it is told to stop, which is either ctx being cancelled or this process being
// signalled - see below.
func RunErr(ctx context.Context, lggr logger.Logger, constructor func(do.Injector) (Capability, error), opts ...Option) error {
	if lggr == nil {
		return errors.New("must provide a logger")
	}

	// TODO: Run should accept some kind of Info struct
	root := &cobra.Command{
		Use:   filepath.Base(os.Args[0]),
		Short: "A CRE capability",
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	lggr = logger.Named(lggr, root.Name())

	cfg := defaultConfig()
	for _, opt := range opts {
		opt(cfg)
	}
	if err := cfg.bind(root); err != nil {
		return err
	}

	root.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run the capability",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// TODO: pass in constructors here for our runnable/embeddable dependencies?
			return run(cmd.Context(), lggr, cmd.Root().Name(), cfg, constructor)
		},
	})

	// TODO: embed command
	root.AddCommand(&cobra.Command{
		Use:   "embed",
		Short: "Embed the capability",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// TODO: pass in constructors here for our runnable/embeddable dependencies?
			// Something else here
			return run(cmd.Context(), lggr, cmd.Root().Name(), cfg, constructor)
		},
	})

	// Wire up SIGTERM + SIGINT to the context
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return root.ExecuteContext(ctx)
}

type Option func(*config)

// WithOtelViews sets otel metric views - histogram bucket boundaries, typically - on the beholder
// client this process reports through.
//
//	capability.Run(ctx, trigger.NewCron, capability.WithOtelViews(trigger.MetricViews()...))
func WithOtelViews(views ...sdkmetric.View) Option {
	return func(c *config) { c.observability.otelViews = append(c.observability.otelViews, views...) }
}
