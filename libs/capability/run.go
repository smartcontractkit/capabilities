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

// Run builds and runs the capability ctor makes, and does not return until ctx is cancelled or the
// process is signalled: a binary that cannot start is a binary that exits, which is the only thing
// a main() can do with the error.
//
//	func main() { capability.Run(context.Background(), trigger.NewCron) }
//
// The logger is this package's own. Use RunErr where the process already has one - a binary that
// built something of its own before this - so that it has one logger rather than two.
func Run(ctx context.Context, ctor any, opts ...Option) {
	lggr, err := NewLogger()
	if err != nil {
		// log rather than logger because we don't have a logger here.
		log.Fatal(err)
	}

	if err := RunErr(ctx, lggr, ctor, opts...); err != nil {
		lggr.Fatal(err)
	}
}

// RunErr is Run, returning the error rather than exiting on it.
//
// A run blocks until it is told to stop, which is either ctx being cancelled or this process being
// signalled - see below. Taking ctx rather than starting from context.Background is what lets a
// caller that already has a lifetime of its own end the run on its terms too: a test, or a binary
// doing something else alongside this.
func RunErr(ctx context.Context, lggr logger.Logger, ctor any, opts ...Option) error {
	if lggr == nil {
		return errors.New("must provide a logger")
	}

	// Read once, here, rather than in the subcommand that calls it. Both `run` and `embed` need the
	// same one, and checking it before the commands exist is what makes a binary whose constructor
	// is not one fail with a sentence about that function - whatever the operator typed, including
	// nothing at all.
	c, err := newConstructor(ctor)
	if err != nil {
		return err
	}

	// TODO: Run should accept some kind of Info struct
	root := &cobra.Command{
		Use:   filepath.Base(os.Args[0]),
		Short: "A CRE capability",
	}
	root.PersistentFlags().String("config", "", "Path to config file")

	// Named after the binary, so every line this process logs says which one it came from - a node
	// running several capability binaries side by side reads them from one stream.
	//
	// Here rather than in Run, so a binary that built its own logger and called RunErr gets it too:
	// what the process is called is not something each caller should have to remember to say.
	lggr = logger.Named(lggr, root.Name())

	// Bound to obs, and decoded before any subcommand runs: registering a target wires that step
	// in. On the root rather than on `run` because these are the process's settings rather than one
	// way of starting it - `embed` wants the same ones, and a setting offered twice is a setting
	// that can be given two values.
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg.observability)
	}
	if err := cfg.register(root); err != nil {
		return err
	}

	// The capability's own config, if its constructor declares one. Under the binary's name
	// rather than bare, so a setting reads as what it configures - cron's fastest schedule is
	// --cron.fastest-schedule-interval-seconds - and cannot collide with a same-named setting in
	// another section.
	if err := c.bindConfig(root); err != nil {
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

	// The signal that stops the process, folded into the caller's context so that either ends the
	// run. A run blocks until this is cancelled - which is what makes the binary a binary rather
	// than something that starts its services and immediately unwinds them.
	//
	// SIGTERM as well as SIGINT: a container runtime asks a process to stop with the former, and a
	// binary that ignored it would be killed rather than shut down.
	//
	// Here rather than in Run so that every caller gets it. A binary embedding this in something
	// larger still has to stop when the operator says so, and that should not be a thing it has to
	// remember to arrange.
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	// ExecuteContext rather than Execute, so cmd.Context() in a subcommand is the one that stops the
	// process rather than a background one that never does.
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

// newConstructor reads ctor: that it is a function, and that what it returns can be hosted.
//
// A constructor may return the capability alone or the capability and an error, since a capability
// that cannot fail to build should not have to pretend it can.
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

// bindConfig creates the config the constructor declares, if any, and binds its fields as flags
// on root, under root's name.
func (c *constructor) bindConfig(root *cobra.Command) error {
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

// call builds the capability, filling each parameter it declares from what the run has built.
//
// Matching is by type rather than by position, so a constructor takes what it needs and says so by
// asking for it: the order it lists things in is its own business, and one that needs nothing takes
// nothing. A parameter nothing answers is reported here, before anything is started, and names the
// type that went unmatched rather than failing somewhere further in.
//
// deps is what this run can offer, and a constructor asks for its fields one at a time rather than
// for the struct - see offered. The one exception is the capability's config, which no dependency
// answers: it is filled from what the flags decoded instead.
func (c constructor) call(deps Dependencies) (Capability, error) {
	available := offered(deps)

	args := make([]reflect.Value, c.typ.NumIn())
	for i := range args {
		if i == c.configIn {
			args[i] = c.config
			continue
		}

		want := c.typ.In(i)

		v, ok := provide(available, want)
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

// offered is what a constructor may ask for: each dependency on its own.
//
// The struct is deliberately not among them. A constructor that took it would declare a dependency
// on everything a run has rather than on what it uses, so its signature would stop saying anything
// and adding a field here would silently widen what every capability appears to need. Naming the
// things it uses is also what lets a capability's tests build those things rather than a whole run.
func offered(deps Dependencies) []any {
	v := reflect.ValueOf(deps)

	available := make([]any, 0, v.NumField())
	for i := range v.NumField() {
		if v.Type().Field(i).IsExported() {
			available = append(available, v.Field(i).Interface())
		}
	}
	return available
}

// provide finds the value in available that can be passed as want.
//
// Assignability rather than equality, so a constructor can ask for an interface it only needs part
// of - the registry as a plain capabilities registry, say - without naming the concrete type the
// run happens to hold.
func provide(available []any, want reflect.Type) (reflect.Value, bool) {
	for _, v := range available {
		if v == nil {
			continue
		}
		if got := reflect.TypeOf(v); got.AssignableTo(want) {
			return reflect.ValueOf(v), true
		}
	}
	return reflect.Value{}, false
}

// Option is something a binary tells Run that its configuration cannot: a value rather than a
// setting, and so not something an operator supplies.
type Option func(*observability)

// WithOtelViews sets otel metric views - histogram bucket boundaries, typically - on the beholder
// client this process reports through.
//
// It is the binary that passes these rather than the capability that needs them, and that is not a
// choice. Per the OTEL specification the buckets have to be defined when the client is created, and
// the client has to exist before the capability is constructed: a capability binds its instruments
// to whatever meter is global at the time. So by the time the capability could hand anything back,
// the client that would have used it has already been built.
//
//	capability.Run(ctx, trigger.NewCron, capability.WithOtelViews(trigger.MetricViews()...))
func WithOtelViews(views ...sdkmetric.View) Option {
	return func(o *observability) { o.otelViews = append(o.otelViews, views...) }
}
