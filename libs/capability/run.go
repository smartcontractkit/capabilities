package capability

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"syscall"

	"github.com/spf13/cobra"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Run builds and runs the capability constructor makes, and does not return until ctx is cancelled or the
// process is signalled.
//
//	func main() { capability.Run(context.Background(), trigger.NewCron) }
func Run(ctx context.Context, constructor any, opts ...Option) {
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
func RunErr(ctx context.Context, lggr logger.Logger, constructor any, opts ...Option) error {
	if lggr == nil {
		return errors.New("must provide a logger")
	}

	// We instantiate this up-front so we can bubble up an error message about an invalid `constructor` early.
	c, err := newConstructor(constructor)
	if err != nil {
		return err
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

	// Bind the capability's config if the constructor function declares a config struct.
	// This will be namespaced using the name of the root command.
	// eg. --cron.fastest-schedule-interval-seconds
	if err := c.bindCapabilityConfig(root); err != nil {
		return err
	}

	root.AddCommand(&cobra.Command{
		Use:   "run",
		Short: "Run the capability",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// TODO: pass in constructors here for our runnable/embeddable dependencies?
			return run(cmd.Context(), lggr, cmd.Root().Name(), cfg, c)
		},
	})

	// TODO: embed command
	root.AddCommand(&cobra.Command{
		Use:   "embed",
		Short: "Embed the capability",
		RunE: func(cmd *cobra.Command, _ []string) error {
			// TODO: pass in constructors here for our runnable/embeddable dependencies?
			// Something else here
			return run(cmd.Context(), lggr, cmd.Root().Name(), cfg, c)
		},
	})

	// Wire up SIGTERM + SIGINT to the context
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	return root.ExecuteContext(ctx)
}

type constructor struct {
	fn  reflect.Value
	typ reflect.Type

	// configIn is the index of the parameter that is the capability's own config, or -1 when the
	// constructor declares none.
	configIn int
	// config is the instance of that parameter the flags decode fills. Kept here rather than
	// passed to run because the decode hook fills it in place when the command runs: call has to
	// read that same memory, not a copy made when it was bound. Valid only when configIn >= 0.
	config reflect.Value
}

var capabilityType = reflect.TypeFor[Capability]()

// newConstructor validates that constructor has the right shape, i.e.:
// - it's a function
// - and the function returns a capability and optionally an error
func newConstructor(ctor any) (constructor, error) {
	t := reflect.TypeOf(ctor)
	if t == nil || t.Kind() != reflect.Func {
		return constructor{}, fmt.Errorf("a capability constructor must be a function, got %T", ctor)
	}

	switch t.NumOut() {
	case 1:
	case 2:
		if t.Out(1) != reflect.TypeFor[error]() {
			return constructor{}, fmt.Errorf(
				"a capability constructor returning two values must return an error second, got %s", t)
		}
	default:
		return constructor{}, fmt.Errorf(
			"a capability constructor must return the capability, and optionally an error, got %s", t)
	}

	if out := t.Out(0); !out.Implements(capabilityType) {
		return constructor{}, fmt.Errorf("%s does not implement capability.Capability", out)
	}

	configIn := -1
	for i := range t.NumIn() {
		if !isConfig(t.In(i)) {
			continue
		}
		if configIn >= 0 {
			return constructor{}, fmt.Errorf("a capability constructor takes its config once, got %s", t)
		}
		configIn = i
	}
	return constructor{fn: reflect.ValueOf(ctor), typ: t, configIn: configIn}, nil
}

var isConfigType = reflect.TypeFor[Config]()

// isConfig reports whether t declares itself a capability's config by embedding Config. The
// embedding has to be direct: the marker declares the struct it is embedded in, not the structs
// that embed that one.
func isConfig(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := range t.NumField() {
		if f := t.Field(i); f.Anonymous && f.Type == isConfigType {
			return true
		}
	}
	return false
}

// bindCapabilityConfig creates the config the constructor declares, if any, and binds its fields as flags
// on root, under root's name.
func (c *constructor) bindCapabilityConfig(root *cobra.Command) error {
	if c.configIn < 0 {
		return nil
	}

	v := reflect.New(c.typ.In(c.configIn))
	fopts := flags.DefaultTOMLOptions("CRE", "CL")
	fopts.Namespace = root.Name()
	if err := flags.RegisterCommandFlags(root, v.Interface(), fopts); err != nil {
		return fmt.Errorf("failed to register the capability's config: %w", err)
	}
	c.config = v.Elem()
	return nil
}

// call builds the capability by calling the contstructor.
// The constructor's parameters are scanned, and type matched against the dependency set.
// The config parameter is treated exceptionally and hydrated from the configuration that was passed in.
func (c constructor) call(deps Dependencies) (Capability, error) {
	args := make([]reflect.Value, c.typ.NumIn())
	for i := range args {
		if i == c.configIn {
			args[i] = c.config
			continue
		}

		want := c.typ.In(i)

		v, ok := deps.resolve(want)
		if !ok {
			return nil, fmt.Errorf("the capability constructor asks for a %s, and nothing in this run "+
				"provides one: %s", want, c.typ)
		}
		args[i] = v
	}

	out := c.fn.Call(args)
	if len(out) == 2 && !out[1].IsNil() {
		return nil, fmt.Errorf("failed to build the capability: %w", out[1].Interface().(error))
	}
	return out[0].Interface().(Capability), nil
}

type Option func(*config)

// WithOtelViews sets otel metric views - histogram bucket boundaries, typically - on the beholder
// client this process reports through.
//
//	capability.Run(ctx, trigger.NewCron, capability.WithOtelViews(trigger.MetricViews()...))
func WithOtelViews(views ...sdkmetric.View) Option {
	return func(c *config) { c.observability.otelViews = append(c.observability.otelViews, views...) }
}
