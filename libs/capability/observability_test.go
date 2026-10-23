package capability

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop/pyroscope"
)

// TestProfilerWithoutAServer is the off case, which is every binary that has not configured
// pyroscope: there is no service at all, rather than one that does nothing.
//
// Nil rather than a no-op matters to the caller: the root would take a no-op and report on it, and
// a health report mentioning profiling that is not happening is worse than not mentioning it.
func TestProfilerWithoutAServer(t *testing.T) {
	assert.Nil(t, newProfiler(logger.Test(t), "cron", pyroscope.PyroscopeConfig{}, false))
}

// TestProfilerWithAServer covers the other side: a configured one is a service the root can take,
// named so it is tellable in a health report.
//
// It is built rather than started. Starting one dials a pyroscope server, which a test has no
// business standing up to prove a constructor returned something.
func TestProfilerWithAServer(t *testing.T) {
	p := newProfiler(logger.Test(t), "cron", pyroscope.PyroscopeConfig{PyroscopeServerAddress: "pyro:4040"}, false)

	require.NotNil(t, p)
	assert.Equal(t, "Profiler", p.Name())
}

// TestProfilerLinksTracesToProfiles makes sure that the profiler wraps the global tracer provider
// only when tracing is on and the config asks for the link. Loop does the same.
//
// Each case sets a tracer provider first, as telemetry does in a run. If the profiler wraps
// otel's default provider, the default provider then calls the wrapper, and the process deadlocks.
func TestProfilerLinksTracesToProfiles(t *testing.T) {
	cfg := pyroscope.PyroscopeConfig{PyroscopeServerAddress: "localhost:1", PyroscopeLinkTracesToProfiles: true}

	for _, tc := range []struct {
		name           string
		tracingEnabled bool
		link           bool
		wantWrapped    bool
	}{
		{name: "tracing and link on", tracingEnabled: true, link: true, wantWrapped: true},
		{name: "tracing off", tracingEnabled: false, link: true, wantWrapped: false},
		{name: "link off", tracingEnabled: true, link: false, wantWrapped: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := otel.GetTracerProvider()
			t.Cleanup(func() { otel.SetTracerProvider(previous) })
			before := noop.NewTracerProvider()
			otel.SetTracerProvider(before)

			cfg.PyroscopeLinkTracesToProfiles = tc.link
			p, err := startProfiler(t.Context(), logger.Test(t), "cron", cfg, tc.tracingEnabled)
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, p.Close()) })

			assert.Equal(t, tc.wantWrapped, otel.GetTracerProvider() != before)
		})
	}
}

// TestWebServerServes is what the HTTP config buys: the endpoints an operator and a prometheus
// scrape depend on, on the configured port, and gone again when it stops.
func TestWebServerServes(t *testing.T) {
	port := freePort(t)
	mux := http.NewServeMux()

	// A route registered before the server is built, which is when a service registers its own.
	mux.HandleFunc("/capability", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "hello")
	})

	web := newWebServer(logger.Test(t), HTTPConfig{Port: uint16(port)}, mux, &probes{})
	require.NoError(t, web.Start(t.Context()))

	for path, want := range map[string]int{
		"/metrics":      http.StatusOK,
		"/debug/pprof/": http.StatusOK,
		"/capability":   http.StatusOK,
	} {
		res, err := http.Get(fmt.Sprintf("http://localhost:%d%s", port, path))
		require.NoError(t, err, path)
		_, _ = io.Copy(io.Discard, res.Body)
		require.NoError(t, res.Body.Close())
		assert.Equal(t, want, res.StatusCode, path)
	}

	// Stopping frees the port, so the next thing to want it can have it.
	require.NoError(t, web.Close())
	_, err := http.Get(fmt.Sprintf("http://localhost:%d/metrics", port))
	assert.Error(t, err, "the server should not answer once it has stopped")
}

// TestWebServerReportsAPortItCannotHave covers the failure a misconfigured port gives: it is
// reported where it happens rather than as a server that silently never listens.
func TestWebServerReportsAPortItCannotHave(t *testing.T) {
	taken, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	// Building it registers routes and binds nothing, so the failure is at start.
	port := uint16(taken.Addr().(*net.TCPAddr).Port)
	web := newWebServer(logger.Test(t), HTTPConfig{Port: port}, http.NewServeMux(), &probes{})

	require.ErrorContains(t, web.Start(t.Context()), fmt.Sprintf("failed to listen on port %d", port))
}
