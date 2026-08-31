package standalone

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// webServer serves one instance's shared mux: /metrics, /debug/pprof, health endpoints, and
// whatever routes a service registered on it during construction.
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

// startWebServer registers /metrics, /debug/pprof and the health endpoints reporting checker's
// view of one instance onto mux, then listens on port and serves it - along with anything already
// registered on mux by a service built earlier in that instance's construction.
func startWebServer(ctx context.Context, lggr logger.Logger, port uint16, mux *http.ServeMux, checker *services.HealthChecker) (*webServer, error) {
	mux.Handle("/metrics", promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// /livez and /healthz report the same signal: /livez is the kubernetes liveness name, /healthz
	// the older combined one kept for anything already probing it.
	mux.HandleFunc("/livez", healthHandler(checker.IsHealthy))
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
// an HTTP handler: 200 when every check passes, 503 otherwise.
//
// The body follows the kubernetes convention. By default it is the failing checks' errors, or
// "ok" when there are none. With ?verbose it is one "[+]name ok" / "[-]name failed: err" line per
// check - passing ones included - followed by a summary line, so a probe URL can be pasted into a
// browser to see what a service actually reports.
func healthHandler(check func() (bool, map[string]error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, errs := check()
		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
		}

		if !isVerbose(r) {
			for name, err := range errs {
				if err != nil {
					fmt.Fprintf(w, "%s: %s\n", name, err)
				}
			}
			if ok {
				fmt.Fprintln(w, "ok")
			}
			return
		}

		// Sorted so repeated polls of the same state produce the same body.
		names := make([]string, 0, len(errs))
		for name := range errs {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			if err := errs[name]; err != nil {
				fmt.Fprintf(w, "[-]%s failed: %s\n", name, err)
			} else {
				fmt.Fprintf(w, "[+]%s ok\n", name)
			}
		}
		if ok {
			fmt.Fprintf(w, "%s check passed\n", strings.TrimPrefix(r.URL.Path, "/"))
		} else {
			fmt.Fprintf(w, "%s check failed\n", strings.TrimPrefix(r.URL.Path, "/"))
		}
	}
}

// isVerbose reports whether the request asked for per-check output. A bare ?verbose counts, as it
// does for kubernetes' own endpoints, and so does any value other than an explicit false/0.
func isVerbose(r *http.Request) bool {
	if !r.URL.Query().Has("verbose") {
		return false
	}
	switch strings.ToLower(r.URL.Query().Get("verbose")) {
	case "false", "0", "no", "off":
		return false
	}
	return true
}
