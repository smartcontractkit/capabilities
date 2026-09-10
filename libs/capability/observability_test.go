package capability

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// TestProfilerWithoutAServer is the off case, which is every binary that has not configured
// pyroscope: there is no service at all, rather than one that does nothing.
//
// Nil rather than a no-op matters to the caller: the root would take a no-op and report on it, and
// a health report mentioning profiling that is not happening is worse than not mentioning it.
func TestProfilerWithoutAServer(t *testing.T) {
	assert.Nil(t, newProfiler(logger.Test(t), "cron", PyroscopeConfig{}))
}

// TestProfilerWithAServer covers the other side: a configured one is a service the root can take,
// named so it is tellable in a health report.
//
// It is built rather than started. Starting one dials a pyroscope server, which a test has no
// business standing up to prove a constructor returned something.
func TestProfilerWithAServer(t *testing.T) {
	p := newProfiler(logger.Test(t), "cron", PyroscopeConfig{ServerAddress: "pyro:4040"})

	require.NotNil(t, p)
	assert.Equal(t, "Profiler", p.Name())
}

// TestWebServerServes is what the HTTP config buys: the endpoints an operator and a prometheus
// scrape depend on, on the configured port, and gone again when it stops.
func TestWebServerServes(t *testing.T) {
	health, err := newHealthChecker(logger.Test(t), beholder.NewNoopClient(), []services.HealthReporter{newFake()})
	require.NoError(t, err)
	require.NoError(t, health.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, health.Close()) })
	checker := health.checker

	port := freePort(t)
	mux := http.NewServeMux()

	// A route registered before the server is built, which is when a service registers its own.
	mux.HandleFunc("/capability", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "hello")
	})

	web := newWebServer(logger.Test(t), HTTPConfig{Port: uint16(port)}, mux, checker)
	require.NoError(t, web.Start(t.Context()))

	for path, want := range map[string]int{
		"/metrics":      http.StatusOK,
		"/debug/pprof/": http.StatusOK,
		"/capability":   http.StatusOK,
		// The capability is built but not started, so it is not ready - which is the honest
		// answer rather than a health check that reports on nothing.
		"/readyz": http.StatusServiceUnavailable,
	} {
		res, err := http.Get(fmt.Sprintf("http://localhost:%d%s", port, path))
		require.NoError(t, err, path)
		_, _ = io.Copy(io.Discard, res.Body)
		require.NoError(t, res.Body.Close())
		assert.Equal(t, want, res.StatusCode, path)
	}

	// Stopping frees the port, so the next thing to want it can have it.
	require.NoError(t, web.Close())
	_, err = http.Get(fmt.Sprintf("http://localhost:%d/metrics", port))
	assert.Error(t, err, "the server should not answer once it has stopped")
}

// TestWebServerReportsAPortItCannotHave covers the failure a misconfigured port gives: it is
// reported where it happens rather than as a server that silently never listens.
func TestWebServerReportsAPortItCannotHave(t *testing.T) {
	health, err := newHealthChecker(logger.Test(t), beholder.NewNoopClient(), []services.HealthReporter{newFake()})
	require.NoError(t, err)
	require.NoError(t, health.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, health.Close()) })
	checker := health.checker

	taken, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	// Building it registers routes and binds nothing, so the failure is at start.
	port := uint16(taken.Addr().(*net.TCPAddr).Port)
	web := newWebServer(logger.Test(t), HTTPConfig{Port: port}, http.NewServeMux(), checker)

	require.ErrorContains(t, web.Start(t.Context()), fmt.Sprintf("failed to listen on port %d", port))
}
