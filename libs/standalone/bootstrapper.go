package standalone

//go:generate go run ./gen

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type Bootstrapper struct {
	root         *cobra.Command
	commonConfig CommonConfig
}

type ServiceFactory interface {
	Run() services.Service
}

// NewBootstrapper creates a new Bootstrapper using the cobra command as its root.
// Note that the RunE on the cobra command will be overwritten when Run is called, and the cobra command is provided only for the remaining fields
func NewBootstrapper(root *cobra.Command) *Bootstrapper {
	bs := &Bootstrapper{root: root}
	root.PersistentFlags().BoolVar(&bs.commonConfig.Fake, "fake", false, "use fake dependencies instead of real ones")
	return bs
}

func (b *Bootstrapper) run(factory func(ctx context.Context) services.Service) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	b.root.RunE = func(cmd *cobra.Command, args []string) error {
		svc := factory(ctx)
		if err := svc.Start(ctx); err != nil {
			stop()
			return err
		}

		<-ctx.Done()
		return nil
	}

	return b.root.Execute()
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
