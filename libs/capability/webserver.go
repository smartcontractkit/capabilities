package capability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/otelhealth"
	"github.com/smartcontractkit/chainlink-common/pkg/services/promhealth"
)

// defaultHTTPPort is where the shared HTTP server listens unless it is told otherwise.
//
// A default rather than a required setting, so that a binary starts with no configuration at all.
// The cost is that two capability binaries on one machine collide unless one of them is told a
// different port - which an operator running more than one is choosing anyway.
const defaultHTTPPort = 8080

// HTTPConfig is the shared HTTP server: /metrics, /debug/pprof, the health endpoints, and whatever
// routes a service registers on the mux while it is being built - it is not only prometheus's, so
// it is named for the transport it serves rather than for one of its handlers.
type HTTPConfig struct {
	Port uint16 `usage:"port serving /metrics, /debug/pprof, /healthz, /readyz and any routes a service registers"`
}

// newWebServer returns the shared HTTP server as a service.
//
// The routes go on the mux here rather than when it starts, so that everything this serves is
// registered before anything is listening: a request cannot arrive for a route that is about to
// exist. Listening is what start does, and what close undoes.
//
// A mux of its own rather than net/http's DefaultServeMux: the mux panics on a second registration
// of the same pattern, so a process serving two of these could not give both a /healthz at all. The
// pprof handlers are registered explicitly for the same reason - importing net/http/pprof only
// installs them on the default mux.
func newWebServer(lggr logger.Logger, cfg HTTPConfig, mux *http.ServeMux, checker *services.HealthChecker) *webService {
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	mux.HandleFunc("/healthz", healthHandler(checker.IsHealthy))
	mux.HandleFunc("/readyz", healthHandler(checker.IsReady))

	w := &webService{
		lggr: lggr,
		port: cfg.Port,
		server: &http.Server{
			Handler: mux,
			// Reasonable default based on a typical prometheus poll interval of 15s.
			ReadTimeout: 5 * time.Second,
		},
	}
	w.Service, _ = services.Config{
		Name:  "WebServer",
		Start: w.start,
		Close: w.close,
	}.NewServiceEngine(lggr)
	return w
}

// webService serves /metrics, /debug/pprof, the health endpoints, and whatever routes a service
// registered on the mux while it was being built.
type webService struct {
	services.Service

	lggr   logger.Logger
	port   uint16
	server *http.Server

	// listener is what start bound, and what close gives back. Written by one hook and read by the
	// other, which the state machine's lock orders.
	listener net.Listener
}

func (w *webService) start(ctx context.Context) error {
	// An explicit listener resolves port 0 before Serve, so the chosen port can be logged.
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", w.port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", w.port, err)
	}
	w.listener = listener

	go func() {
		if err := w.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Sugared(w.lggr).Errorw("Metrics and health server stopped", "err", err)
		}
	}()

	w.lggr.Infow("Serving metrics and health endpoints", "address", listener.Addr().String())
	return nil
}

func (w *webService) close() error {
	err := w.server.Close()

	// The listener too, and not only through the server: Close closes the listeners the server is
	// already serving, and the goroutine above may not have got that far. Without this, close can
	// return while the port is still bound - so a caller that stops one server and starts another
	// on the same port sometimes fails, depending on scheduling.
	//
	// Closing twice is why the error is dropped: whichever of the two got there first has already
	// done the work, and the second reports the socket it wanted to close is closed.
	_ = w.listener.Close()

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// newHealthChecker returns the health checker as a service.
//
// The checker is made here rather than when the service starts, so that it can be handed to the web
// server that serves its view before either of them is running. Making one polls nothing: it is
// starting that does, and a checker that was never started has nothing to stop.
//
// It reports on its siblings rather than on the service that contains it, and that is what lets it
// be one of them. Register reads a reporter's health as it registers, and the checker only re-reads
// on a 15s tick - so registering the parent, which is still starting while its own sub-services
// run, would seed "not started" and leave /readyz wrong for a quarter of a minute. Registering the
// services added before it has no such problem: sub-services start in order, so they are already
// running by the time this one does.
//
// client is always a client, never nil: when telemetry is off it is the noop one that is global
// until something replaces it, so the otel hooks are configured either way and simply record
// nothing.
func newHealthChecker(lggr logger.Logger, client *beholder.Client, reporters []services.HealthReporter) (*healthService, error) {
	cfg := promhealth.ConfigureHooks(services.HealthCheckerConfig{})
	newCfg, err := otelhealth.ConfigureHooks(cfg, client.Meter)
	if err != nil {
		return nil, fmt.Errorf("failed to configure health checker otel hooks: %w", err)
	}
	cfg = newCfg

	h := &healthService{checker: cfg.New(), reporters: reporters}
	h.Service, _ = services.Config{
		Name:  "HealthChecker",
		Start: h.start,
		Close: h.close,
	}.NewServiceEngine(lggr)
	return h, nil
}

// healthService owns a services.HealthChecker, which mirrors its reporters as prometheus metrics
// ("health", "uptime_seconds", "version") and, when telemetry is configured, as otel metrics
// through the same beholder client the rest of the process reports over.
//
// The checker is wrapped rather than used directly because it is not a services.Service: its Start
// takes no context, and it reports no health of its own.
type healthService struct {
	services.Service

	// checker is made when this is built, so that whatever serves its view can be given it without
	// waiting for this to start.
	checker   *services.HealthChecker
	reporters []services.HealthReporter
}

func (h *healthService) start(context.Context) error {
	if err := h.checker.Start(); err != nil {
		return fmt.Errorf("failed to start health checker: %w", err)
	}
	// Registering reads a reporter's health as it goes, which is why this is here rather than at
	// construction: everything being reported on has to be running by now.
	for _, r := range h.reporters {
		if err := h.checker.Register(r); err != nil {
			// Started but not recorded as started, so nothing else will stop it.
			return errors.Join(
				fmt.Errorf("failed to register %s with the health checker: %w", r.Name(), err),
				h.checker.Close())
		}
	}
	return nil
}

func (h *healthService) close() error { return h.checker.Close() }

// healthHandler adapts a services.HealthChecker.IsHealthy/IsReady-shaped func into an HTTP handler:
// 200 with each check's status when ok, 503 and the failing checks' errors otherwise.
func healthHandler(check func() (bool, map[string]error)) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		ok, errs := check()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		for name, err := range errs {
			if err != nil {
				fmt.Fprintf(w, "%s: %s\n", name, err)
			}
		}
		if ok {
			fmt.Fprintln(w, "ok")
		}
	}
}
