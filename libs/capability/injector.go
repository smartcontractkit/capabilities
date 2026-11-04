package capability

import (
	"fmt"
	"slices"

	"github.com/samber/do/v2"
	typetostring "github.com/samber/go-type-to-string"
	"github.com/smartcontractkit/chainlink-common/x/config/cli"
	"github.com/spf13/cobra"
)

// Injector wraps [do.Injector] providing access only to [do.Provide] and like methods as [do.ProvideValue]
// cannot be overwritten on clones. Injector also provides helpers for the CLI registration by gathering all config types.
type Injector struct {
	injector         *do.RootScope
	directlyProvided map[string]bool
	// configs is a slice of items over map to keep ordering.
	configs []injectedConfig
}

type injectedConfig struct {
	name     string
	value    any
	register func(cmd *cobra.Command, b *cli.Binder) error
}

func NewInjector() *Injector {
	return &Injector{
		injector:         do.New(),
		directlyProvided: map[string]bool{},
	}
}

// Provide see [do.Provide], if you're registering a config, use [Injector.ProvideConfig]
// Allows overriding on a cloned scope.
func (s *Injector) Provide[T any](provider do.Provider[T]) error {
	// borrowed from do.Provide
	return s.ProvideNamed(typetostring.GetType[T](), provider)
}

// ProvideValue provides v as an already-built value. Unlike [do.ProvideValue], it can be overridden on a clone.
func (s *Injector) ProvideValue[T any](v T) error {
	return s.Provide(func(do.Injector) (T, error) { return v, nil })
}

// ProvideNamedValue provides v under name as an already-built value. Unlike [do.ProvideNamedValue], it can be
// overridden on a clone.
func (s *Injector) ProvideNamedValue[T any](name string, v T) error {
	return s.ProvideNamed(name, func(do.Injector) (T, error) { return v, nil })
}

// ProvideNamed see [do.ProvideNamed], if you're registering a config, use [Injector.ProvideConfig]
func (s *Injector) ProvideNamed[T any](name string, provider do.Provider[T]) error {
	if s.directlyProvided[name] {
		return fmt.Errorf("%s is already provided", name)
	}

	s.directlyProvided[name] = true

	do.OverrideNamed(s.injector, name, provider)

	return nil
}

// ProvideConfig wraps the config in a method so it can be used with [Injector.Provide],
// and tracks it for registration with the CLI.
// The namespace is only used for the CLI registration.
func (s *Injector) ProvideConfig[T any](namespace string, conf *T, opts ...cli.RegisterOption[T]) error {
	// borrowed from do.Provide
	return s.ProvideNamedConfig(namespace, typetostring.GetType[*T](), conf, opts...)
}

// ProvideNamedConfig wraps the config in a method so it can be used with [Injector.Provide],
// // and tracks it for registration with the CLI.
// // The namespace is only used for the CLI registration.
func (s *Injector) ProvideNamedConfig[T any](namespace, diName string, conf *T, opts ...cli.RegisterOption[T]) error {
	if err := s.ProvideNamed(diName, func(provider do.Injector) (*T, error) { return conf, nil }); err != nil {
		return err
	}

	c := injectedConfig{
		name:  diName,
		value: conf,
		register: func(cmd *cobra.Command, b *cli.Binder) error {
			return b.RegisterInNamespace(cmd, namespace, conf, opts...)
		},
	}

	i := slices.IndexFunc(s.configs, func(c injectedConfig) bool { return c.name == diName })
	if i < 0 {
		s.configs = append(s.configs, c)
	} else {
		s.configs[i] = c
	}

	return nil
}

// MustProvide is [Injector.Provide], panicking on error.
func (s *Injector) MustProvide[T any](provider do.Provider[T]) {
	must(s.Provide(provider))
}

// MustProvideValue is [Injector.ProvideValue], panicking on error.
func (s *Injector) MustProvideValue[T any](v T) {
	must(s.ProvideValue(v))
}

// MustProvideNamed is [Injector.ProvideNamed], panicking on error.
func (s *Injector) MustProvideNamed[T any](name string, provider do.Provider[T]) {
	must(s.ProvideNamed(name, provider))
}

// MustProvideNamedValue is [Injector.ProvideNamedValue], panicking on error.
func (s *Injector) MustProvideNamedValue[T any](name string, v T) {
	must(s.ProvideNamedValue(name, v))
}

// MustProvideConfig is [Injector.ProvideConfig], panicking on error.
func (s *Injector) MustProvideConfig[T any](namespace string, conf *T, opts ...cli.RegisterOption[T]) {
	must(s.ProvideConfig(namespace, conf, opts...))
}

// MustProvideNamedConfig is [Injector.ProvideNamedConfig], panicking on error.
func (s *Injector) MustProvideNamedConfig[T any](namespace, diName string, conf *T, opts ...cli.RegisterOption[T]) {
	must(s.ProvideNamedConfig(namespace, diName, conf, opts...))
}

// Do is the underlying injector, which is what constructors resolve their dependencies from.
func (s *Injector) Do() do.Injector {
	return s.injector
}

func (s *Injector) RegisterConfig(cmd *cobra.Command, binder *cli.Binder) error {
	for _, c := range s.configs {
		if err := c.register(cmd, binder); err != nil {
			return err
		}
	}

	return nil
}

// Configs are the configs provided with [Injector.ProvideConfig] and [Injector.ProvideNamedConfig].
func (s *Injector) Configs() []any {
	configs := make([]any, len(s.configs))
	for i, c := range s.configs {
		configs[i] = c.value
	}
	return configs
}

// Clone copies all providers and configs into a new Injector.
// Newly cloned scopes can override values provided by the original scope
// Clones are not kept up to date if later values are added to the original.
func (s *Injector) Clone() *Injector {
	return &Injector{
		// do.RootScope.Clone resets lazy services, so a service resolved in the clone uses any overrides made in the clone.
		injector:         s.injector.Clone(),
		directlyProvided: map[string]bool{},
		configs:          slices.Clone(s.configs),
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
