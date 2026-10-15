package capability

import (
	"fmt"
	"maps"
	"slices"

	"github.com/samber/do/v2"
	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/x/config/cli"
)

// registrations is what a binary's packages register, from init, for one command: the providers
// each instance's injector is built from, and the configs bound as the command's flags.
//
// Nothing is resolved here. Each instance gets an injector of its own, built from these, so a
// provider depending on what the run provides - the logger, the registry - resolves against that
// instance's.
type registrations struct {
	providers []func(do.Injector)
	// names are the services provided, so a second registration of one is refused rather than
	// panicking in do when an instance's injector is built.
	names map[string]bool
	// configs are keyed by namespace, which is how they are bound and documented.
	configs map[string]boundConfig
}

// boundConfig is one config: the struct its flags decode into, and how to bind it.
type boundConfig struct {
	value any
	bind  func(*cli.Binder, *cobra.Command) error
}

func newRegistrations() *registrations {
	return &registrations{names: map[string]bool{}, configs: map[string]boundConfig{}}
}

// runRegistrations and embedRegistrations are what the run and embed commands build each
// instance's injector from.
var runRegistrations = newRegistrations()
var embedRegistrations = newRegistrations()

// Provide registers provider for both commands. See ProvideForRun.
//
//	func init() { capability.Provide(NewDB) }
func Provide[T any](provider do.Provider[T]) {
	ProvideForRun(provider)
	ProvideForEmbed(provider)
}

// ProvideForRun registers provider for the run command: every run instance's injector provides T
// from it. Providing a T twice for one command panics - it is a programming error, and init is
// where it would be made.
func ProvideForRun[T any](provider do.Provider[T]) { must(provide(runRegistrations, provider)) }

// ProvideForEmbed is ProvideForRun for the embed command, so an embedded instance can be provided
// a T of its own.
func ProvideForEmbed[T any](provider do.Provider[T]) { must(provide(embedRegistrations, provider)) }

// ProvideConfig registers a config for both commands. See ProvideConfigForRun.
//
//	func init() { capability.ProvideConfig("database", DefaultConfig) }
func ProvideConfig[T any](namespace string, defaultConfig T, opts ...cli.RegisterOption[T]) {
	ProvideConfigForRun(namespace, defaultConfig, opts...)
	ProvideConfigForEmbed(namespace, defaultConfig, opts...)
}

// ProvideConfigForRun declares a T, defaulting to defaultConfig, as config of the run command: its
// fields are bound as flags under namespace (--database.url, CRE_DATABASE_URL, or the key in a
// database section of the config file), and every run instance's injector provides the decoded T.
// opts are handed to the binder - a profile, say. Registering a namespace or a T twice for one
// command panics.
func ProvideConfigForRun[T any](namespace string, defaultConfig T, opts ...cli.RegisterOption[T]) {
	must(provideConfig(runRegistrations, namespace, defaultConfig, opts...))
}

// ProvideConfigForEmbed is ProvideConfigForRun for the embed command.
func ProvideConfigForEmbed[T any](namespace string, defaultConfig T, opts ...cli.RegisterOption[T]) {
	must(provideConfig(embedRegistrations, namespace, defaultConfig, opts...))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func provide[T any](r *registrations, provider do.Provider[T]) error {
	name := do.NameOf[T]()
	if r.names[name] {
		return fmt.Errorf("capability: %s is already provided", name)
	}
	r.names[name] = true
	r.providers = append(r.providers, func(i do.Injector) { do.Provide(i, provider) })
	return nil
}

// provideConfig wraps the config into a function that binds it on a command, and a provider that
// reads what the flags decoded into it.
func provideConfig[T any](r *registrations, namespace string, defaultConfig T, opts ...cli.RegisterOption[T]) error {
	if _, ok := r.configs[namespace]; ok {
		return fmt.Errorf("capability: config namespace %q is already registered", namespace)
	}

	// The struct the flags decode into, filled in place when the command runs - after this, and
	// before anything resolves it.
	cfg := new(T)
	*cfg = defaultConfig
	if err := provide(r, func(do.Injector) (T, error) { return *cfg, nil }); err != nil {
		return err
	}

	r.configs[namespace] = boundConfig{
		value: cfg,
		bind: func(b *cli.Binder, cmd *cobra.Command) error {
			return b.RegisterInNamespace(cmd, namespace, cfg, opts...)
		},
	}
	return nil
}

// bind binds every config r holds on cmd, in namespace order so a binary's flags do not depend on
// map iteration.
func (r *registrations) bind(b *cli.Binder, cmd *cobra.Command) error {
	for _, namespace := range slices.Sorted(maps.Keys(r.configs)) {
		if err := r.configs[namespace].bind(b, cmd); err != nil {
			return fmt.Errorf("failed to bind config %q: %w", namespace, err)
		}
	}
	return nil
}

// injector builds a fresh injector from r, for one instance.
func (r *registrations) injector() *do.RootScope {
	i := do.New()
	for _, p := range r.providers {
		p(i)
	}
	return i
}

// provideValue provides v on an instance's injector, refusing a type the binary already
// registered: what the run provides is not replaceable.
func provideValue[T any](r *registrations, i do.Injector, v T) error {
	if name := do.NameOf[T](); r.names[name] {
		return fmt.Errorf("%s is provided by the run, and also registered by this binary", name)
	}
	do.ProvideValue(i, v)
	return nil
}
