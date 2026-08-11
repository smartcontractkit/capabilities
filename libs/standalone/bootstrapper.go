package standalone

//go:generate go run ./gen

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"reflect"
	"strconv"
	"sync"
	"syscall"

	"github.com/hashicorp/go-plugin"
	"github.com/prometheus/client_golang/prometheus"
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

// StandaloneConfig holds the per-instance dependencies the Bootstrapper provides
// to the service factory passed to Run.
type StandaloneConfig struct {
	// Logger is hclog-compatible like a LOOP plugin's: JSON on stderr with
	// @level/@message/@timestamp keys, so a go-plugin host (e.g. the core node)
	// can parse and re-level the entries when this process runs under one,
	// while remaining plain JSON logs when run standalone. Named after the
	// instance when this process runs more than one.
	Logger logger.SugaredLogger

	// BeholderClient is the process-wide telemetry client, nil unless telemetry is configured.
	// Exposed so a factory can build its own otel instruments (meters, tracers) consistent with
	// the rest of the process, the same way a LOOP plugin would reach beholder.GetClient().
	BeholderClient *beholder.Client

	// MetricsRegisterer is where prometheus collectors belong. It is the default registerer for
	// a single-instance run, and one wrapped with an "instance" label otherwise: registering the
	// same collector twice fails, so without the label only the first instance's metrics would
	// be recorded.
	MetricsRegisterer prometheus.Registerer
}

type Bootstrapper struct {
	root          *cobra.Command
	settings      settings
	config        *StandaloneConfig
	commonConfig  CommonConfig
	embedConfig   embedConfig
	observability observability

	profiler *pyroscope.Profiler // nil unless a pyroscope server is configured

	closersMu sync.Mutex
	closers   []io.Closer // resolved dependency values and started instances that implement io.Closer
}

// NewBootstrapper creates a new Bootstrapper using the cobra command as its root. The root is
// there to describe the binary - its name, its help, its own settings - and to hang the "run" and
// "embed" subcommands off when Run is called; it does not run anything itself.
//
// It creates the hclog-compatible logger and registers the process-wide configuration - common
// settings plus telemetry, tracing, chip ingress, profiling and the metrics/health server. The
// values those configure are started when the command runs, since nothing is decoded until then.
// The logger runs and supervises the services (health, lifecycle logging) and is available via
// Logger for use before Run.
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

	bs := &Bootstrapper{
		root:          root,
		settings:      s,
		config:        &StandaloneConfig{Logger: slggr, MetricsRegisterer: prometheus.DefaultRegisterer},
		observability: defaultObservability(),
		embedConfig:   defaultEmbedConfig(),
	}

	// Registered through pkg/config/flags like every other config struct, and at the top level
	// since what it holds is process-wide rather than one dependency's. It has no settings at the
	// moment, so this binds nothing; it stays so that the next one is bound, documented and
	// reachable from every dependency without further wiring.
	if err := flags.RegisterCommandFlags(root, &bs.commonConfig, flags.DefaultTOMLOptions("CRE", "CL")); err != nil {
		slggr.Fatalf("Failed to register common flags: %s", err)
	}
	for _, o := range bs.observability.namespaced() {
		opts := flags.DefaultTOMLOptions("CRE", "CL")
		opts.Namespace = o.namespace
		if err := flags.RegisterCommandFlags(root, o.target, opts); err != nil {
			slggr.Fatalf("Failed to register %s flags: %s", o.namespace, err)
		}
	}
	return bs
}

// close closes started instances and resolved dependencies (in reverse resolution
// order), stops profiling, flushes telemetry, and syncs logs: the counterpart to
// the setup in NewBootstrapper and to dependency resolution during run.
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

// instanceServices builds the services of one instance, given that instance's StandaloneConfig.
type instanceServices func(ctx context.Context, cfg *StandaloneConfig) []services.Service

// instantiator returns the factory building instance index's services. The generated Run helpers
// supply it, since only they know the dependencies' types; embed says whether to replace each
// dependency with its embedded form (BootstrapDependency.ForEmbedding) first.
type instantiator func(index int, embed bool) instanceServices

// run wires up the subcommands that start the binary and executes the root command.
//
// Each subcommand runs instantiate once per instance, composing that instance's services into a
// single supervising service via services.Engine sub-services, so their health is aggregated the
// same way the rest of the stack does it (services.Config.NewSubServices + HealthReport). It
// starts them along with a health checker (registered against the aggregated root service) and,
// if a prometheus port is configured, that instance's web server serving /metrics,
// /debug/pprof and /healthz + /readyz, then blocks until an interrupt and closes everything in
// reverse.
func (b *Bootstrapper) run(instantiate instantiator, configured, embedded []BootstrapCommand) error {
	defer b.close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Run a single instance",
		Long: `Runs one instance, resolving every dependency as it is configured.

Its settings are its own: a dependency that an embedded instance replaces rather than configures
(rage networking, say) takes its settings here and nowhere else.`,
		RunE: func(*cobra.Command, []string) error {
			return b.runInstances(ctx, stop, 1, false, instantiate)
		},
	}

	embedCmd := &cobra.Command{
		Use:   "embed",
		Short: "Run several instances in this process, with the networking between them skipped",
		Long: `Runs --instances copies of everything "run" runs inside a single process.

Dependencies serve each instance rather than being configured per instance: the transports between
instances are replaced by in-process ones, identities that would be read from a database are derived
from the instance index instead, and what state instances do keep is partitioned per instance. Each
instance reports its own health, on the configured prometheus port plus its index.

So the settings here are not "run" settings plus a count: what an embedded instance derives or
replaces, it cannot be told, and only what it still needs is accepted.`,
		RunE: func(*cobra.Command, []string) error {
			return b.runInstances(ctx, stop, b.embedConfig.Instances, true, instantiate)
		},
	}

	// Local to this subcommand: --instances means nothing to a single instance.
	if err := flags.RegisterSubcommandFlags(embedCmd, "", &b.embedConfig, flags.DefaultTOMLOptions("CRE", "CL")); err != nil {
		return err
	}

	if err := b.setupCommands(runCmd, embedCmd, configured, embedded); err != nil {
		return err
	}

	// The root only describes the binary: it has no RunE, so a bare invocation prints the help
	// listing the two ways to start it rather than silently picking one of them.
	b.root.AddCommand(runCmd, embedCmd)

	return b.root.Execute()
}

// runInstances starts count instances and blocks until shutdown. stop cancels ctx, so a failure
// part-way through starting them tears down the ones already running rather than leaving them
// wedged.
func (b *Bootstrapper) runInstances(ctx context.Context, stop context.CancelFunc, count int, embed bool, instantiate instantiator) error {
	if count < 1 {
		return fmt.Errorf("cannot run %d instances: at least one is required", count)
	}

	// Telemetry and profiling are started here rather than in NewBootstrapper because their
	// configuration is not decoded until the command runs. Both are process-wide, so they are
	// started once however many instances follow, and before any of them create instruments.
	beholderClient, err := startTelemetry(ctx, b.observability, b.settings.otelViews)
	if err != nil {
		return fmt.Errorf("failed to start telemetry: %w", err)
	}
	b.config.BeholderClient = beholderClient

	b.profiler, err = startProfiler(b.root.Name(), b.observability.pyroscope)
	if err != nil {
		return fmt.Errorf("failed to start profiler: %w", err)
	}

	for i := range count {
		if err := b.startInstance(ctx, i, count, instantiate(i, embed)); err != nil {
			stop()
			return fmt.Errorf("failed to start instance %d: %w", i, err)
		}
	}

	if !embed && underPluginHost() {
		// Launched by a go-plugin host (e.g. the core node): expose the empty
		// LOOP so the host can supervise this process over gRPC (handshake +
		// go-plugin's liveness health check). The started services run in
		// this process, so that liveness reflects them. Blocks until the host
		// shuts us down.
		//
		// Not for an embed run: a host supervises one plugin, and the instances of an embed run
		// are this process's own business rather than something it can address.
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

// startInstance starts one instance's services, health checker and web server, registering each
// for shutdown. Everything it registers is closed in reverse order by close, which puts an
// instance's services ahead of the dependencies they resolved during start.
func (b *Bootstrapper) startInstance(ctx context.Context, index, count int, factory instanceServices) error {
	cfg := b.instanceConfig(index, count)

	root, _ := services.Config{
		Name:           instanceName(index, count),
		NewSubServices: func(logger.Logger) []services.Service { return factory(ctx, cfg) },
	}.NewServiceEngine(cfg.Logger)

	if err := root.Start(ctx); err != nil {
		return err
	}
	b.registerCloser(root)

	checker, err := b.startHealthChecker(root)
	if err != nil {
		return err
	}
	b.registerCloser(checker)

	if b.observability.prometheus.disabled() {
		return nil
	}
	// Instance i takes the configured port plus i, so several instances can serve their own
	// health without being configured a port each.
	web, err := startWebServer(ctx, cfg.Logger, b.observability.prometheus.portFor(index), checker)
	if err != nil {
		return err
	}
	b.registerCloser(web)
	return nil
}

// instanceConfig is the StandaloneConfig handed to one instance's factory: the process-wide values,
// plus the logger and registerer that belong to that instance alone.
//
// It deliberately does not say which instance this is. A service is written once and knows nothing
// about embedding; everything that has to differ between instances is a dependency, replaced by its
// embedded form before the service is handed it (see BootstrapDependency.ForEmbedding).
func (b *Bootstrapper) instanceConfig(index, count int) *StandaloneConfig {
	cfg := *b.config
	if count == 1 {
		return &cfg
	}

	cfg.Logger = logger.Sugared(logger.Named(b.config.Logger, "instance."+strconv.Itoa(index)))
	// Constant label rather than a registry per instance: the health metrics come from
	// package-level promauto collectors on the default registry, which a private registry would
	// not see, and registering the same collector a second time is an error.
	cfg.MetricsRegisterer = prometheus.WrapRegistererWith(
		prometheus.Labels{"instance": strconv.Itoa(index)}, prometheus.DefaultRegisterer)
	return &cfg
}

// instanceName names an instance's aggregated service. It carries the index when there is more
// than one instance, since the health metrics are labelled by service name and would otherwise
// collide between instances.
func instanceName(index, count int) string {
	if count == 1 {
		return "Bootstrap"
	}
	return "Bootstrap." + strconv.Itoa(index)
}

// setupCommands registers the configuration of every dependency on the command that resolves it:
// the dependencies as configured under `run`, the ones embedding replaces them with under `embed`,
// and anything both forms share on the root, where both inherit it.
//
// Registering per subcommand is what keeps each command's settings honest. A setting an embedded
// instance derives rather than reads - a listen address, a keystore password - is not offered
// under `embed` at all, instead of being offered and then quietly ignored; and a setting only an
// embedded instance has is offered, which it could not be if only the configured form were
// registered.
//
// A config instance is registered exactly once. Two commands binding one key would both point
// viper's binding for it at whichever registered last, so the value typed on the command actually
// running would be read from the other command's untouched flag - hence the shared ones going to
// the root rather than to each subcommand.
func (b *Bootstrapper) setupCommands(runCmd, embedCmd *cobra.Command, configured, embedded []BootstrapCommand) error {
	configuredTargets, err := collectTargets(configured)
	if err != nil {
		return err
	}
	embeddedTargets, err := collectTargets(embedded)
	if err != nil {
		return err
	}
	configuredConfigs, embeddedConfigs := configSet(configuredTargets), configSet(embeddedTargets)

	for _, t := range configuredTargets {
		// Shared by both forms - one dependency serving every instance, typically - so it belongs
		// to the binary rather than to either way of starting it.
		cmd := runCmd
		if embeddedConfigs[t.config] {
			cmd = b.root
		}
		if err := b.registerTarget(cmd, t); err != nil {
			return err
		}
	}

	for _, t := range embeddedTargets {
		if configuredConfigs[t.config] {
			continue // registered on the root above
		}
		if err := b.registerTarget(embedCmd, t); err != nil {
			return err
		}
	}
	return nil
}

// registerTarget binds t's settings to cmd: persistently when that is the root, so subcommands
// inherit them, and locally otherwise.
func (b *Bootstrapper) registerTarget(cmd *cobra.Command, t target) error {
	opts := flags.DefaultTOMLOptions("CRE", "CL")
	opts.Namespace = t.namespace
	if cmd == b.root {
		return flags.RegisterCommandFlags(b.root, t.config, opts)
	}
	// opts.Namespace prefixes the flags as well as the keys, so a dependency's setting is
	// --ocr.listen-addresses wherever it is registered: it is named after the dependency that owns
	// it, not after the command that happens to accept it.
	return flags.RegisterSubcommandFlags(cmd, t.namespace, t.config, opts)
}

// target is one config instance to register, under the namespace of the dependency that owns it.
type target struct {
	namespace string
	config    any
}

// collectTargets walks commands and everything they depend on, in declaration order, returning each
// distinct config instance once - so a dependency two others share contributes its settings once
// rather than colliding with itself.
//
// A config must be a pointer, since it is what the configuration is decoded into, or nil when the
// dependency has nothing to configure. Anything else is rejected here rather than left to fail
// further along: it would also be unusable as a map key, which is how instances are told apart.
func collectTargets(commands []BootstrapCommand) ([]target, error) {
	var targets []target
	seen := map[any]bool{}

	var walk func(cmds []BootstrapCommand) error
	walk = func(cmds []BootstrapCommand) error {
		for _, cmd := range cmds {
			if cmd == nil {
				continue
			}

			switch cfg := cmd.Config(); {
			case cfg == nil: // nothing to configure
			case reflect.ValueOf(cfg).Kind() != reflect.Pointer:
				return fmt.Errorf("%T: Config must return a pointer to the settings, or nil, got %T", cmd, cfg)
			case !seen[cfg]:
				seen[cfg] = true
				targets = append(targets, target{namespace: cmd.Namespace(), config: cfg})
			}

			if err := walk(cmd.Dependencies()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(commands); err != nil {
		return nil, err
	}

	return targets, nil
}

// configSet indexes targets by config instance, for asking whether the other form registers one too.
func configSet(targets []target) map[any]bool {
	configs := make(map[any]bool, len(targets))
	for _, t := range targets {
		configs[t.config] = true
	}
	return configs
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

// CommonConfig is the process-wide configuration every dependency is handed when it is resolved.
// It says nothing about which instance is resolving it: by then a dependency has already been
// replaced by the form that serves that instance, so there is no decision left for it to make.
type CommonConfig struct{}

// embedConfig is the `embed` subcommand's own configuration.
type embedConfig struct {
	Instances int `usage:"number of instances to run in this process"`
}

func defaultEmbedConfig() embedConfig { return embedConfig{Instances: 1} }

type Dependency[T any] interface {
	Get(ctx context.Context) (T, error)
}

type BootstrapCommand interface {
	// Config returns the settings to bind, as a pointer to the struct they are decoded into, or nil
	// when there are none - which is the usual answer from an embedded dependency, having derived or
	// replaced everything it would otherwise be told.
	Config() any

	Dependencies() []BootstrapCommand

	// Namespace roots this dependency's configuration, so its settings group together and
	// same-named settings from different dependencies don't collide - "database" gives
	// --database.url, the key database.url and the env var CRE_DATABASE_URL. Return "" to
	// keep the settings at the top level.
	Namespace() string
}

type BootstrapDependency[T any] interface {
	Get(ctx context.Context, c CommonConfig) (T, error)

	// ForEmbedding returns the dependency instance i of an embedded run resolves instead of this
	// one. It is called once per instance, after the configuration has been decoded and before
	// anything is resolved, and only for an embedded run - a single instance resolves the
	// dependency exactly as configured.
	//
	// What it returns is already specific to instance i: everything embedding changes - a derived
	// identity instead of a stored one, an in-process transport instead of a socket, a schema of
	// its own instead of the shared one - is settled here, so Get has no instance to ask about and
	// no mode to branch on. A dependency whose embedded form needs none of its settings is free to
	// return something else entirely (see ocr.Host), which is also how a setting that only a real
	// deployment needs stops being required.
	//
	// Return the receiver to be shared by every instance, which is what a dependency backed by
	// one process-wide resource wants: sharing the dependency shares the single value it resolves
	// to. Anything else returns a copy, deep-copying the configuration it adapts so that instances
	// never write through to each other's settings.
	ForEmbedding(i int) BootstrapDependency[T]

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

// instanceOf resolves which dependency instance index uses, and wraps it for that instance's
// factory. Called by the generated Run helpers, once per dependency per instance.
//
// A single instance keeps the dependency as it was built rather than embedding it at index 0:
// `run` is what the configuration describes literally, and a dependency that partitions itself per
// instance should not quietly move a single run's state somewhere else.
func instanceOf[T any](bs *Bootstrapper, bd BootstrapDependency[T], index int, embed bool) *dependency[T] {
	if embed {
		bd = bd.ForEmbedding(index)
	}
	return &dependency[T]{bs: bs, bd: bd}
}

// OnceBootstrapper wraps a BootstrapDependency so that Get is evaluated at most
// onceGet: the first call resolves the dependency and caches its (value, error),
// and every subsequent call returns that same result without re-running Get
// (the ctx and CommonConfig of later calls are ignored). Other commands are all delegated
//
// BootstrapDependency implementations are shared and may have Get called more
// than onceGet — e.g. one dependency resolving another, or the same dependency
// feeding several services — so a New function should wrap its dependency with
// OnceBootstrapper before returning it, making repeated Get calls safe and
// side-effect-free.
func OnceBootstrapper[T any](bd BootstrapDependency[T]) BootstrapDependency[T] {
	return &onceBootstrapper[T]{BootstrapDependency: bd}
}

type onceBootstrapper[T any] struct {
	BootstrapDependency[T]
	once sync.Once
	val  T
	err  error
}

func (o *onceBootstrapper[T]) Get(ctx context.Context, c CommonConfig) (T, error) {
	o.once.Do(func() { o.val, o.err = o.BootstrapDependency.Get(ctx, c) })
	return o.val, o.err
}

// ForEmbedding gives each instance its own cache, since each resolves its own value - unless the
// wrapped dependency returns itself, meaning it is shared by every instance, in which case this
// wrapper (and the one value it caches) is shared too.
func (o *onceBootstrapper[T]) ForEmbedding(i int) BootstrapDependency[T] {
	next := o.BootstrapDependency.ForEmbedding(i)
	if next == o.BootstrapDependency {
		return o
	}
	return OnceBootstrapper[T](next)
}
