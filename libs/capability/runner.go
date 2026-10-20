package capability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/hashicorp/go-plugin"
	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/capabilities/libs/capability/di"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// instance is one capability a run builds: the injector it is built from, the logger it is given, and
// the registry it resolves other capabilities through.
type instance struct {
	lggr         logger.Logger
	injector     *di.Injector
	capabilities CapabilitiesConfig
}

func run(ctx context.Context, lggr logger.Logger, name string, cfg *config, newCapability func(do.Injector) (Capability, error), instances ...instance) error {
	defer func() { _ = lggr.Sync() }()

	var svcs []services.Service
	defer func() {
		if err := services.MultiCloser(svcs).Close(); err != nil {
			logger.Sugared(lggr).Errorw("failed to stop the services of this run", "err", err)
		}
	}()

	mux := http.NewServeMux()

	profiler, err := startProfiler(ctx, lggr, name, cfg.observability.pyroscope)
	if err != nil {
		return fmt.Errorf("failed to start profiler: %w", err)
	}
	if profiler != nil {
		svcs = append(svcs, profiler)
	}

	telemetry, err := startTelemetry(ctx, lggr, &cfg.observability)
	if err != nil {
		return fmt.Errorf("failed to build telemetry: %w", err)
	}
	svcs = append(svcs, telemetry)

	settings, err := startSettings(ctx, lggr, cfg.settings)
	if err != nil {
		return fmt.Errorf("failed to start settings: %w", err)
	}
	svcs = append(svcs, settings)

	for _, inst := range instances {
		reg, err := startRegistry(ctx, inst.lggr, inst.capabilities, cfg.grpc)
		if err != nil {
			return fmt.Errorf("failed to start registry: %w", err)
		}
		svcs = append(svcs, reg)

		capability, err := startCapability(ctx, inst.lggr, inst.injector, newCapability, reg, settings.values)
		if err != nil {
			return err
		}
		svcs = append(svcs, capability)
	}

	health, err := startHealthChecker(ctx, lggr, beholder.GetClient(), servicesToHealthReporters(svcs))
	if err != nil {
		return fmt.Errorf("failed to start health checker: %w", err)
	}
	svcs = append(svcs, health)

	ws, err := startWebServer(ctx, lggr, cfg.observability.http, mux, health.checker)
	if err != nil {
		return fmt.Errorf("failed to start web server: %w", err)
	}
	svcs = append(svcs, ws)

	if underPluginHost() {
		lggr.Info("Serving the empty LOOP: this process is supervised by a go-plugin host")
		plugin.Serve(&plugin.ServeConfig{
			HandshakeConfig: emptyHandshakeConfig(),
			Plugins:         map[string]plugin.Plugin{emptyPluginName: &emptyLoop{}},
			GRPCServer:      plugin.DefaultGRPCServer,
		})
		return nil
	}

	<-ctx.Done()
	lggr.Info("Shutting down")
	return nil
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

// startCapability builds the capability from its constructor and makes it reachable: started,
// served and announced by the registry, and mounted via the debug UI if debug mode is turned on.
func startCapability(ctx context.Context, lggr logger.Logger, i *di.Injector, ctor func(do.Injector) (Capability, error), reg *registryService, settings *loop.AtomicSettings) (Capability, error) {
	// The run supervises the services it provides, so the injector is never shut down: doing so
	// would close them a second time.
	if err := errors.Join(
		i.ProvideValue(lggr),
		i.ProvideValue[core.CapabilitiesRegistry](reg),
		i.ProvideValue(newLimitsFactory(lggr, settings)),
		i.ProvideValue(clockwork.NewRealClock()),
	); err != nil {
		return nil, fmt.Errorf("failed to provide the run's dependencies: %w", err)
	}

	c, err := ctor(i.Do())
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate capability: %w", err)
	}
	if c == nil {
		return nil, errors.New("failed to instantiate capability: the constructor returned neither a capability nor an error")
	}

	if err := c.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start capability: %w", err)
	}

	if err := reg.Add(ctx, c); err != nil {
		// Not yet on the caller's list, so the deferred close would never reach it.
		_ = c.Close()
		return nil, err
	}

	return c, nil
}
