package capability

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"

	"github.com/smartcontractkit/capabilities/libs/capability/di"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// RunInjector is what the run command builds a capability from. Register what a capability needs on it
// before calling Run; the run adds its own dependencies to it.
var RunInjector = di.NewInjector()

// EmbedInjector is what the embed command builds each of its instances from. Every instance gets a clone
// of it, so each resolves its providers itself and can override them. A clone is not a deep copy: a value
// or config provided here is the same object in every instance, so provide a constructor for anything an
// instance must not share.
var EmbedInjector = di.NewInjector()

// Injectors provides to both RunInjector and EmbedInjector, for what a capability needs however it is run.
var Injectors = di.MultiInjector{RunInjector, EmbedInjector}

// InstanceInfo is which of a run's instances a capability is. A run command has one.
type InstanceInfo struct {
	// ID is this instance's index, from 0 to Total-1.
	ID int
	// Total is how many instances the run has.
	Total int
}

type embedConfig struct {
	capabilities CapabilitiesConfig
	grpc         GRPCConfig
	settings     *loop.AtomicSettings
	overrides    []func(*di.Injector) error
}

type EmbedOption func(*embedConfig)

// WithProvider calls provide on a clone of each instance's injector just before the capability is built,
// so what it provides overrides what Embed provides, and a later WithProvider overrides it in turn.
func WithProvider(provide func(*di.Injector) error) EmbedOption {
	return func(c *embedConfig) { c.overrides = append(c.overrides, provide) }
}

func withNode(capabilities CapabilitiesConfig, grpc GRPCConfig, settings *loop.AtomicSettings) EmbedOption {
	return func(c *embedConfig) {
		c.capabilities = capabilities
		c.grpc = grpc
		c.settings = settings
	}
}

// Embed builds and starts total instances of the capability ctor builds, each from a clone of base, without
// the process around them. It also returns every service it started, in the order it started them; the
// caller closes them, even when Embed fails.
func Embed[T Capability](ctx context.Context, lggr logger.Logger, base *di.Injector, total int,
	ctor func(do.Injector) (T, error), opts ...EmbedOption) ([]T, []services.Service, error) {
	if total < 1 {
		return nil, nil, fmt.Errorf("instances must be positive, got %d", total)
	}

	cfg := &embedConfig{grpc: defaultConfig().grpc, settings: newSettings(lggr)}
	for _, opt := range opts {
		opt(cfg)
	}

	var built []T
	var svcs []services.Service
	for id := range total {
		instLggr := lggr
		if total > 1 {
			instLggr = logger.Named(lggr, fmt.Sprintf("Instance%d", id))
		}

		reg, err := startRegistry(ctx, instLggr, cfg.capabilities, cfg.grpc)
		if err != nil {
			return built, svcs, fmt.Errorf("failed to start registry: %w", err)
		}
		svcs = append(svcs, reg)

		c, err := startCapability(ctx, instLggr, base.Clone(), InstanceInfo{ID: id, Total: total}, ctor, reg, cfg)
		if err != nil {
			return built, svcs, err
		}
		built = append(built, c)
		svcs = append(svcs, c)
	}
	return built, svcs, nil
}

// startCapability builds the capability from its constructor and makes it reachable.
func startCapability[T Capability](ctx context.Context, lggr logger.Logger, i *di.Injector, info InstanceInfo,
	ctor func(do.Injector) (T, error), reg *registryService, cfg *embedConfig) (T, error) {
	var zero T

	if err := errors.Join(
		i.ProvideValue(info),
		i.ProvideValue(lggr),
		i.ProvideValue[registry.CapabilitiesRegistry](reg),
		i.ProvideValue(newLimitsFactory(lggr, cfg.settings)),
		i.ProvideValue[clockwork.Clock](clockwork.NewRealClock()),
	); err != nil {
		return zero, fmt.Errorf("failed to provide the run's dependencies: %w", err)
	}

	for _, override := range cfg.overrides {
		i = i.Clone()
		if err := override(i); err != nil {
			return zero, fmt.Errorf("failed to override the run's dependencies: %w", err)
		}
	}

	c, err := ctor(i.Do())
	if err != nil {
		return zero, fmt.Errorf("failed to instantiate capability: %w", err)
	}

	// Reflection is needed because something like Capability((*CronServer)(nil)) == nil is false
	if v := reflect.ValueOf(c); !v.IsValid() || (v.Kind() == reflect.Pointer && v.IsNil()) {
		return zero, errors.New("failed to instantiate capability: the constructor returned neither a capability nor an error")
	}

	if err = c.Start(ctx); err != nil {
		return zero, fmt.Errorf("failed to start capability: %w", err)
	}

	if err = reg.Add(ctx, c); err != nil {
		// Not yet on the caller's list, so the deferred close would never reach it.
		_ = c.Close()
		return zero, err
	}

	return c, nil
}
