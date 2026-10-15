package capability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
	"github.com/smartcontractkit/chainlink-common/x/config/cli"
)

// start brings up what the process shares - profiler, telemetry, the CRE settings the host pushes -
// and then total instances over it, each with an injector of its own built from r, and returns how
// to stop them all.
//
// A failure part-way stops whatever had started.
func start(ctx context.Context, lggr logger.Logger, name string, opts runOptions, r *registrations, b *cli.Binder, total int, ctor Constructor) (_ func(), err error) {
	var svcs []services.Service
	stop := func() {
		if err := services.MultiCloser(svcs).Close(); err != nil {
			logger.Sugared(lggr).Errorw("failed to stop the services of this run", "err", err)
		}
	}
	defer func() {
		if err != nil {
			stop()
		}
	}()

	// The run's own settings are configs, read through what the flags decoded into, so an injector
	// built only to read them builds nothing else.
	cfg, err := resolveSettings(r.injector())
	if err != nil {
		return nil, err
	}
	cfg.observability.otelViews = opts.otelViews

	profiler, err := startProfiler(ctx, lggr, name, cfg.pyroscope)
	if err != nil {
		return nil, fmt.Errorf("failed to start profiler: %w", err)
	}
	if profiler != nil {
		svcs = append(svcs, profiler)
	}

	telemetry, err := startTelemetry(ctx, lggr, &cfg.observability)
	if err != nil {
		return nil, fmt.Errorf("failed to build telemetry: %w", err)
	}
	svcs = append(svcs, telemetry)

	settings, err := startSettings(ctx, lggr, cfg.settings)
	if err != nil {
		return nil, fmt.Errorf("failed to start settings: %w", err)
	}
	svcs = append(svcs, settings)

	for id := range total {
		started, err := startInstance(ctx, lggr, r, b, Instance{ID: id, Total: total}, cfg, settings.values, ctor)
		svcs = append(svcs, started...)
		if err != nil {
			return nil, fmt.Errorf("instance %d: %w", id, err)
		}
	}

	return stop, nil
}

// startInstance brings up one instance, on an injector of its own built from r: its Instance, the
// run's values as this instance sees them, the capability, and the health checker and web server
// reporting on them. It returns what it started, even on failure, so the caller stops it.
//
// Several instances are told apart: each logs as instance.N, and serves on http.port + N.
func startInstance(ctx context.Context, lggr logger.Logger, r *registrations, b *cli.Binder, instance Instance, cfg runSettings, settings *loop.AtomicSettings, ctor Constructor) (svcs []services.Service, err error) {
	if instance.Total > 1 {
		lggr = logger.Named(lggr, "instance."+strconv.Itoa(instance.ID))
	}

	reg, err := startRegistry(ctx, lggr, cfg.capabilities, cfg.grpc)
	if err != nil {
		return svcs, fmt.Errorf("failed to start registry: %w", err)
	}
	svcs = append(svcs, reg)

	i := r.injector()
	if err := errors.Join(
		provideValue(r, i, instance),
		provideValue(r, i, lggr),
		// core.CapabilitiesRegistry is an alias of registry.CapabilitiesRegistry, so this is both.
		provideValue[registry.CapabilitiesRegistry](r, i, reg),
		provideValue(r, i, newLimitsFactory(lggr, settings)),
		provideValue[clockwork.Clock](r, i, clockwork.NewRealClock()),
		// Nil until a run can build one; every consumer already tolerates a missing resolver.
		provideValue[orgresolver.OrgResolver](r, i, nil),
		provideValue(r, i, b),
	); err != nil {
		return svcs, err
	}

	capability, err := startCapability(ctx, i, ctor, reg)
	if err != nil {
		return svcs, err
	}
	svcs = append(svcs, capability)

	health, err := startHealthChecker(ctx, lggr, beholder.GetClient(), servicesToHealthReporters(svcs))
	if err != nil {
		return svcs, fmt.Errorf("failed to start health checker: %w", err)
	}
	svcs = append(svcs, health)

	mux := http.NewServeMux()

	httpCfg := cfg.http
	httpCfg.Port += uint16(instance.ID)
	ws, err := startWebServer(ctx, lggr, httpCfg, mux, health.checker)
	if err != nil {
		return svcs, fmt.Errorf("failed to start web server: %w", err)
	}
	svcs = append(svcs, ws)

	return svcs, nil
}

// runSettings is what start resolves of its own settings.
type runSettings struct {
	observability observability
	pyroscope     PyroscopeConfig
	http          HTTPConfig
	capabilities  capabilityConfig
	grpc          grpcConfig
	settings      settingsConfig
}

// resolveSettings resolves the run's settings from i.
func resolveSettings(i do.Injector) (cfg runSettings, err error) {
	if err := errors.Join(
		resolve(i, &cfg.observability.telemetry),
		resolve(i, &cfg.observability.tracing),
		resolve(i, &cfg.observability.chipIngress),
		resolve(i, &cfg.pyroscope),
		resolve(i, &cfg.http),
		resolve(i, &cfg.grpc),
		resolve(i, &cfg.capabilities),
		resolve(i, &cfg.settings),
	); err != nil {
		return runSettings{}, fmt.Errorf("failed to resolve the run's settings: %w", err)
	}
	return cfg, nil
}

// resolve fills target with the T i resolves.
func resolve[T any](i do.Injector, target *T) (err error) {
	*target, err = do.Invoke[T](i)
	return err
}

func servicesToHealthReporters(svcs []services.Service) []services.HealthReporter {
	reporters := make([]services.HealthReporter, 0, len(svcs))
	for _, s := range svcs {
		reporters = append(reporters, s)
	}
	return reporters
}

// underPluginHost reports whether this process was launched by a go-plugin host, detected via the
// empty plugin's handshake magic cookie.
//
// The check is necessary rather than defensive: go-plugin's Serve refuses to run - and exits the
// process - when the cookie is absent, so a standalone binary that called it would die on startup.
func underPluginHost() bool {
	h := emptyHandshakeConfig()
	return os.Getenv(h.MagicCookieKey) == h.MagicCookieValue
}

// startCapability builds a capability with ctor from the instance's injector and makes it
// reachable: started, served and announced by the registry.
func startCapability(ctx context.Context, i do.Injector, ctor Constructor, reg *registryService) (Capability, error) {
	c, err := ctor(i)
	if err != nil {
		return nil, fmt.Errorf("failed to build the capability: %w", err)
	}
	if c == nil {
		return nil, errors.New("failed to build the capability: the constructor returned none")
	}

	if err = c.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start capability: %w", err)
	}

	if err = reg.Add(ctx, c); err != nil {
		// Not yet on the caller's list, so the deferred close would never reach it.
		_ = c.Close()
		return nil, err
	}

	return c, nil
}
