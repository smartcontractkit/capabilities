package standalone

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

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// webServer serves one instance's /metrics, /debug/pprof and health endpoints.
//
// Each instance gets its own mux and listener rather than sharing net/http's DefaultServeMux (as
// loop.WebServerOpts does): the mux panics on a second registration of the same pattern, so a
// process running several instances could not give each of them a /healthz at all. The pprof
// handlers are therefore registered explicitly too, since importing net/http/pprof only installs
// them on the default mux.
type webServer struct {
	lggr     logger.Logger
	server   *http.Server
	listener net.Listener
}

// startWebServer listens on port and serves the endpoints reporting checker's view of one
// instance. Port 0 asks the OS for an ephemeral port, which is logged once bound.
func startWebServer(ctx context.Context, lggr logger.Logger, port int, checker *services.HealthChecker) (*webServer, error) {
	mux := http.NewServeMux()

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

	// An explicit listener resolves port 0 before Serve, so the chosen port can be logged.
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	w := &webServer{
		lggr:     lggr,
		listener: listener,
		server: &http.Server{
			Handler: mux,
			// Reasonable default based on a typical prometheus poll interval of 15s.
			ReadTimeout: 5 * time.Second,
		},
	}

	go func() {
		if err := w.server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			w.lggr.Errorw("Metrics and health server stopped", "err", err)
		}
	}()

	lggr.Infow("Serving metrics and health endpoints", "address", listener.Addr().String())
	return w, nil
}

func (w *webServer) Close() error {
	// Closes the listener too, so the port is free again as soon as this returns.
	if err := w.server.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// healthHandler adapts a services.HealthChecker.IsHealthy/IsReady-shaped func into
// an HTTP handler: 200 with each check's status when ok, 503 and the failing
// checks' errors otherwise.
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
