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
	"github.com/smartcontractkit/chainlink-common/pkg/loop/telemetry"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

func run(ctx context.Context, lggr logger.Logger, name string, cfg *config, newCapability func(do.Injector) (Capability, error), base *Injector, total int, capabilities CapabilitiesConfig) error {
	defer func() { _ = lggr.Sync() }()

	var svcs []services.Service
	defer func() {
		if err := services.MultiCloser(svcs).Close(); err != nil {
			logger.Sugared(lggr).Errorw("failed to stop the services of this run", "err", err)
		}
	}()

	mux := http.NewServeMux()

	profiler, err := startProfiler(ctx, lggr, name, cfg.observability.PyroscopeConfig)
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

	// Start telemetry before you make the gRPC options. The gRPC tracing and metrics read the
	// telemetry providers when you make them.
	grpcOpts := cfg.grpc.New(lggr)

	settings, err := startSettings(ctx, lggr, cfg.settings)
	if err != nil {
		return fmt.Errorf("failed to start settings: %w", err)
	}
	svcs = append(svcs, settings)

	_, started, err := StartCapabilities(ctx, lggr, base, total, newCapability, withNode(capabilities, cfg.grpc, &grpcOpts, settings.values))
	svcs = append(svcs, started...)
	if err != nil {
		return err
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

func startTelemetry(ctx context.Context, lggr logger.Logger, obs *observability) (*telemetry.Telemetry, error) {
	t, err := telemetry.NewTelemetry(lggr, obs.TelemetryConfig, "", obs.otelViews)
	if err != nil {
		return nil, err
	}

	return t, t.Start(ctx)
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
