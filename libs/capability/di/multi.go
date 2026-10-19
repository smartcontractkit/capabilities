package di

import (
	"errors"

	"github.com/samber/do/v2"
	"github.com/smartcontractkit/chainlink-common/x/config/cli"
)

// MultiInjector provides the same thing to each of its injectors.
type MultiInjector []*Injector

// Provide calls [Injector.Provide] on each injector.
func (m MultiInjector) Provide[T any](provider do.Provider[T]) error {
	return m.each(func(i *Injector) error { return i.Provide(provider) })
}

// ProvideValue calls [Injector.ProvideValue] on each injector.
func (m MultiInjector) ProvideValue[T any](v T) error {
	return m.each(func(i *Injector) error { return i.ProvideValue(v) })
}

// ProvideNamed calls [Injector.ProvideNamed] on each injector.
func (m MultiInjector) ProvideNamed[T any](name string, provider do.Provider[T]) error {
	return m.each(func(i *Injector) error { return i.ProvideNamed(name, provider) })
}

// ProvideNamedValue calls [Injector.ProvideNamedValue] on each injector.
func (m MultiInjector) ProvideNamedValue[T any](name string, v T) error {
	return m.each(func(i *Injector) error { return i.ProvideNamedValue(name, v) })
}

// ProvideConfig calls [Injector.ProvideConfig] on each injector. They share conf, which is safe because
// only one of their commands runs in a process.
func (m MultiInjector) ProvideConfig[T any](namespace string, conf *T, opts ...cli.RegisterOption[T]) error {
	return m.each(func(i *Injector) error { return i.ProvideConfig(namespace, conf, opts...) })
}

// ProvideNamedConfig calls [Injector.ProvideNamedConfig] on each injector, sharing conf as
// [MultiInjector.ProvideConfig] does.
func (m MultiInjector) ProvideNamedConfig[T any](namespace, diName string, conf *T, opts ...cli.RegisterOption[T]) error {
	return m.each(func(i *Injector) error { return i.ProvideNamedConfig(namespace, diName, conf, opts...) })
}

// each calls fn on every injector, joining their errors.
func (m MultiInjector) each(fn func(*Injector) error) error {
	errs := make([]error, 0, len(m))
	for _, i := range m {
		errs = append(errs, fn(i))
	}

	return errors.Join(errs...)
}
