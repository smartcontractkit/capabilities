package capability

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/jonboulle/clockwork"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/do/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// RunInjector is what the run command builds a capability from. Register what a capability needs on it
// before calling Run; the run adds its own dependencies to it.
var RunInjector = NewInjector()

// EmbedInjector is what the embed command builds each of its instances from. Every instance gets a clone
// of it, so each resolves its providers itself and can override them. A clone is not a deep copy: a value
// or config provided here is the same object in every instance, so provide a constructor for anything an
// instance must not share.
var EmbedInjector = NewInjector()

// Injectors provides to both RunInjector and EmbedInjector, for what a capability needs however it is run.
var Injectors = MultiInjector{RunInjector, EmbedInjector}

// InstanceInfo is which of a run's instances a capability is. A run command has one.
type InstanceInfo struct {
	// ID is this instance's index, from 0 to Total-1.
	ID int
	// Total is how many instances the run has.
	Total int
}

type startConfig struct {
	capabilities CapabilitiesConfig
	grpc         GRPCConfig
	grpcOpts     *loop.GRPCOpts
	settings     *loop.AtomicSettings
	overrides    []func(*Injector) error
}

type StartOption func(*startConfig)

// WithProvider calls provide on a clone of each instance's injector just before the capability is built,
// so what it provides overrides what StartCapabilities provides, and a later WithProvider overrides it in
// turn.
func WithProvider(provide func(*Injector) error) StartOption {
	return func(c *startConfig) { c.overrides = append(c.overrides, provide) }
}

func withNode(capabilities CapabilitiesConfig, grpc GRPCConfig, grpcOpts *loop.GRPCOpts, settings *loop.AtomicSettings) StartOption {
	return func(c *startConfig) {
		c.capabilities = capabilities
		c.grpc = grpc
		c.grpcOpts = grpcOpts
		c.settings = settings
	}
}

// StartCapabilities builds and starts total instances of the capability ctor builds, each from a clone of
// base, without the process around them. It also returns every service it started, in the order it started
// them; the caller closes them, even when StartCapabilities fails.
func StartCapabilities[T Capability](ctx context.Context, lggr logger.Logger, base *Injector, total int,
	ctor func(do.Injector) (T, error), opts ...StartOption) ([]T, []services.Service, error) {
	if total < 1 {
		return nil, nil, fmt.Errorf("instances must be positive, got %d", total)
	}

	cfg := &startConfig{grpc: defaultConfig("TEST_APP").grpc, settings: newSettings(lggr)}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.grpcOpts == nil {
		// Only run() gives gRPC options. Other callers, for example capabilitytest, come here.
		// Tests call this function many times in one process. Each call must use a new registry,
		// because the default registry panics if you register the same metrics two times.
		grpcOpts := loop.GRPCOptsConfig{Registerer: prometheus.NewRegistry()}.New(lggr)
		cfg.grpcOpts = &grpcOpts
	}

	var built []T
	var svcs []services.Service
	for id := range total {
		instLggr := lggr
		if total > 1 {
			instLggr = logger.Named(lggr, fmt.Sprintf("Instance%d", id))
		}

		reg, err := startRegistry(ctx, instLggr, cfg.capabilities, cfg.grpc, *cfg.grpcOpts)
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
func startCapability[T Capability](ctx context.Context, lggr logger.Logger, i *Injector, info InstanceInfo,
	ctor func(do.Injector) (T, error), reg *registryService, cfg *startConfig) (T, error) {
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
