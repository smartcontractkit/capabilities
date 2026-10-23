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
	"github.com/smartcontractkit/chainlink-common/pkg/loop/telemetry"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

func run(ctx context.Context, lggr logger.Logger, name string, cfg *config, newCapability func(do.Injector) (Capability, error), base *Injector, total int, capabilities CapabilitiesConfig) error {
	defer func() { _ = lggr.Sync() }()

	// Started first and closed last, so probes get an answer while the run starts and stops.
	p := &probes{}
	ws, err := startWebServer(ctx, lggr, cfg.observability.http, http.NewServeMux(), p)
	if err != nil {
		return fmt.Errorf("failed to start web server: %w", err)
	}
	defer func() {
		if err := ws.Close(); err != nil {
			logger.Sugared(lggr).Errorw("failed to stop the web server", "err", err)
		}
	}()

	var svcs []services.Service
	defer func() {
		p.draining.Store(true)
		if err := services.MultiCloser(svcs).Close(); err != nil {
			logger.Sugared(lggr).Errorw("failed to stop the services of this run", "err", err)
		}
	}()
	started := func(s ...services.Service) {
		svcs = append(svcs, s...)
		p.add(servicesToHealthReporters(s)...)
	}

	telemetry, err := startTelemetry(ctx, lggr, &cfg.observability)
	if err != nil {
		return fmt.Errorf("failed to build telemetry: %w", err)
	}
	started(telemetry)

	streamed, err := streamLogs(lggr, telemetry)
	if err != nil {
		return err
	}
	lggr = streamed

	// Start the profiler after telemetry. To link traces to profiles, the profiler wraps the tracer
	// provider that telemetry sets.
	profiler, err := startProfiler(ctx, lggr, name, cfg.observability.PyroscopeConfig, cfg.observability.TracingEnabled)
	if err != nil {
		return fmt.Errorf("failed to start profiler: %w", err)
	}
	if profiler != nil {
		started(profiler)
	}

	// Start telemetry before you make the gRPC options. The gRPC tracing and metrics read the
	// telemetry providers when you make them.
	grpcOpts := cfg.grpc.New(lggr)

	settings, err := startSettings(ctx, lggr, cfg.settings, grpcOpts)
	if err != nil {
		return fmt.Errorf("failed to start settings: %w", err)
	}
	started(settings)

	_, caps, err := StartCapabilities(ctx, lggr, base, total, newCapability, withNode(capabilities, cfg.grpc, &grpcOpts, settings.values))
	started(caps...)
	if err != nil {
		return err
	}

	health, err := startHealthChecker(ctx, lggr, beholder.GetClient(), servicesToHealthReporters(svcs))
	if err != nil {
		return fmt.Errorf("failed to start health checker: %w", err)
	}
	started(health)

	p.started.Store(true)

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
	t, err := telemetry.NewTelemetry(lggr, obs.TelemetryConfig, obs.AppID, obs.otelViews)
	if err != nil {
		return nil, err
	}

	return t, t.Start(ctx)
}

// streamLogs returns a logger that also sends its logs to the beholder client, if the telemetry
// config turns on log streaming. Otherwise it returns lggr. Loop does the same.
func streamLogs(lggr logger.Logger, t *telemetry.Telemetry) (logger.Logger, error) {
	level, ok := t.LogStreamingLevel()
	if !ok {
		return lggr, nil
	}

	otelLggr, err := loop.NewOtelLogger(t.Client().Logger, level)
	if err != nil {
		return nil, fmt.Errorf("failed to enable log streaming: %w", err)
	}
	return logger.Named(otelLggr, lggr.Name()), nil
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
