package capability

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"

	"github.com/hashicorp/go-plugin"
	"github.com/samber/do/v2"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/loop/telemetry"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// runner runs a capability with its services, until the run must stop.
type runner struct {
	lggr          logger.Logger
	name          string
	cfg           *config
	newCapability func(do.Injector) (Capability, error)
	injector      *Injector
	total         int
	capabilities  CapabilitiesConfig
	web           HTTPConfig
	// serve blocks until the run must stop.
	serve func(context.Context)
}

// newRunner returns a runner. If serve is nil, the runner waits until ctx is done.
func newRunner(lggr logger.Logger, name string, cfg *config, newCapability func(do.Injector) (Capability, error),
	injector *Injector, total int, capabilities CapabilitiesConfig, web HTTPConfig, serve func(context.Context)) runner {
	if serve == nil {
		serve = func(ctx context.Context) { <-ctx.Done() }
	}
	return runner{
		lggr:          lggr,
		name:          name,
		cfg:           cfg,
		newCapability: newCapability,
		injector:      injector,
		total:         total,
		capabilities:  capabilities,
		web:           web,
		serve:         serve,
	}
}

func (r runner) run(ctx context.Context) error {
	lggr := r.lggr
	defer func() { _ = lggr.Sync() }()

	// Started first and closed last, so probes get an answer while the run starts and stops.
	p := &probes{}
	ws, err := startWebServer(ctx, lggr, r.web, http.NewServeMux(), p)
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
		stopInReverse(lggr, svcs)
	}()
	started := func(s ...services.Service) {
		svcs = append(svcs, s...)
		p.add(servicesToHealthReporters(s)...)
	}

	telemetry, err := startTelemetry(ctx, lggr, &r.cfg.observability)
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
	profiler, err := startProfiler(ctx, lggr, r.name, r.cfg.observability.PyroscopeConfig, r.cfg.observability.TracingEnabled)
	if err != nil {
		return fmt.Errorf("failed to start profiler: %w", err)
	}
	if profiler != nil {
		started(profiler)
	}

	// Start telemetry before you make the gRPC options. The gRPC tracing and metrics read the
	// telemetry providers when you make them.
	grpcOpts := r.cfg.grpc.New(lggr)

	settings, err := startSettings(ctx, lggr, r.cfg.settings, grpcOpts)
	if err != nil {
		return fmt.Errorf("failed to start settings: %w", err)
	}
	started(settings)

	_, caps, err := StartCapabilities(ctx, lggr, r.injector, r.total, r.newCapability, withNode(r.capabilities, r.cfg.grpc, &grpcOpts, settings.values))
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

	r.serve(ctx)
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

// stopInReverse stops each service before the services that it depends on. Each service starts
// after its dependencies, so the reverse of the start order is the reverse of the dependency order.
func stopInReverse(lggr logger.Logger, svcs []services.Service) {
	for _, s := range slices.Backward(svcs) {
		if err := s.Close(); err != nil {
			logger.Sugared(lggr).Errorw("failed to stop a service of this run", "service", s.Name(), "err", err)
		}
	}
}

func servicesToHealthReporters(svcs []services.Service) []services.HealthReporter {
	reporters := make([]services.HealthReporter, 0, len(svcs))
	for _, s := range svcs {
		reporters = append(reporters, s)
	}
	return reporters
}

func servePlugin(lggr logger.Logger) {
	lggr.Info("Serving the empty LOOP: this process is supervised by a go-plugin host")
	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: emptyHandshakeConfig(),
		Plugins:         map[string]plugin.Plugin{emptyPluginName: &emptyLoop{}},
		GRPCServer:      plugin.DefaultGRPCServer,
	})
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
