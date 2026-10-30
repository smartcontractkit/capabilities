package capability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"

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
	serve func(context.Context, services.Service)
}

// newRunner returns a runner. If serve is nil, the runner waits until ctx is done.
func newRunner(lggr logger.Logger, name string, cfg *config, newCapability func(do.Injector) (Capability, error),
	injector *Injector, total int, capabilities CapabilitiesConfig, web HTTPConfig, serve func(context.Context, services.Service)) runner {
	if serve == nil {
		serve = func(ctx context.Context, s services.Service) { <-ctx.Done() }
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

	root := newRootService(lggr)
	if err := root.Start(ctx); err != nil {
		return err
	}

	defer func() {
		err := root.Close()
		if err != nil {
			lggr.Errorf("failed to close root service: %s", err)
		}
	}()

	// Started first and closed last, so probes get an answer while the run starts and stops.
	ws, err := startWebServer(ctx, lggr, r.web, http.NewServeMux(), root.probes)
	if err != nil {
		return fmt.Errorf("failed to start web server: %w", err)
	}
	root.addServices(ws)

	telemetry, err := startTelemetry(ctx, lggr, &r.cfg.observability)
	if err != nil {
		return fmt.Errorf("failed to build telemetry: %w", err)
	}
	root.addServices(telemetry)

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
		root.addServices(profiler)
	}

	// Start telemetry before you make the gRPC options. The gRPC tracing and metrics read the
	// telemetry providers when you make them.
	grpcOpts := r.cfg.grpc.New(lggr)

	settings, err := startSettings(ctx, lggr, r.cfg.settings, grpcOpts)
	if err != nil {
		return fmt.Errorf("failed to start settings: %w", err)
	}
	root.addServices(settings)

	_, caps, err := StartCapabilities(ctx, lggr, r.injector, r.total, r.newCapability, withNode(r.capabilities, r.cfg.grpc, &grpcOpts, settings.values))
	root.addServices(caps...)
	if err != nil {
		return err
	}

	health, err := startHealthChecker(ctx, lggr, beholder.GetClient(), servicesToHealthReporters(root.subServices))
	if err != nil {
		return fmt.Errorf("failed to start health checker: %w", err)
	}
	root.addServices(health)

	root.probes.started.Store(true)
	r.serve(ctx, root)
	lggr.Info("Shutting down")
	return nil
}

func newRootService(lggr logger.Logger) *rootService {
	rs := &rootService{lggr: lggr, probes: &probes{}}
	rs.Service, _ = services.Config{
		Name:  "Root",
		Close: rs.close,
	}.NewServiceEngine(lggr)
	return rs
}

// rootService is the run as one service: the go-plugin host supervises it in compatibility mode.
// It does not start its services, because each one starts as soon as it is built, so that the next
// one can use it.
type rootService struct {
	services.Service
	lggr        logger.Logger
	probes      *probes
	subServices []services.Service
}

func (r *rootService) addServices(svcs ...services.Service) {
	r.subServices = append(r.subServices, svcs...)
	r.probes.add(servicesToHealthReporters(svcs)...)
}

// close stops each service before the services that it depends on. Each service starts after its
// dependencies, so the reverse of the start order is the reverse of the dependency order.
func (r *rootService) close() error {
	r.probes.draining.Store(true)
	var errs []error
	for _, s := range slices.Backward(r.subServices) {
		if err := s.Close(); err != nil {
			logger.Sugared(r.lggr).Errorw("failed to stop a service of this run", "service", s.Name(), "err", err)
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// HealthReport adds the health of the run's services, which the engine does not know about.
func (r *rootService) HealthReport() map[string]error {
	m := r.Service.HealthReport()
	for _, s := range r.subServices {
		services.CopyHealth(m, s.HealthReport())
	}
	return m
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
	h := loop.EmptyHandshakeConfig()
	return os.Getenv(h.MagicCookieKey) == h.MagicCookieValue
}
