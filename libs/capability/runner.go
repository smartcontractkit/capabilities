package capability

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/hashicorp/go-plugin"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

func run(ctx context.Context, lggr logger.Logger, name string, cfg *config, newCapability func(do.Injector) (Capability, error)) error {
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

	reg, err := startRegistry(ctx, lggr, cfg.capabilities, cfg.grpc)
	if err != nil {
		return fmt.Errorf("failed to start registry: %w", err)
	}
	svcs = append(svcs, reg)

	settings, err := startSettings(ctx, lggr, cfg.settings)
	if err != nil {
		return fmt.Errorf("failed to start settings: %w", err)
	}
	svcs = append(svcs, settings)

	capability, err := startCapability(ctx, lggr, cfg, newCapability, reg, settings.values, mux)
	if err != nil {
		return err
	}
	svcs = append(svcs, capability)

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
func startCapability(ctx context.Context, lggr logger.Logger, cfg *config, ctor func(do.Injector) (Capability, error), reg *registryService, settings *loop.AtomicSettings, mux *http.ServeMux) (Capability, error) {
	// The run supervises the services it provides, so the injector is never shut down: doing so
	// would close them a second time.
	i := do.New()
	do.ProvideValue(i, lggr)
	do.ProvideValue[core.CapabilitiesRegistry](i, reg)
	do.ProvideValue(i, newLimitsFactory(lggr, settings))

	c, err := ctor(i)
	if err != nil {
		return nil, fmt.Errorf("failed to instantiate capability: %w", err)
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
