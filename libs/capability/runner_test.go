package capability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

// runArgs is the arguments of one run over settings a test can start: a real port, and everything
// else off.
func runArgs(t *testing.T, ctor any) (*config, constructor, uint16) {
	t.Helper()

	c, err := newConstructor(ctor)
	require.NoError(t, err)

	port := uint16(freePort(t))
	cfg := defaultConfig()
	cfg.observability.http.Port = port
	// A stub the registrar's announcement can land on: registering the capability dials the
	// proxy, so it has to be a real listener rather than merely a lazy one.
	cfg.capabilities.ProxyURL = serveStubRegistry(t, &stubRegistry{adds: map[string]string{}})

	return cfg, c, port
}

// runToCompletion starts a run and stops it once it is serving, returning what run returned.
//
// A run blocks until its context is cancelled, so a test that wants one to finish has to be the
// thing that ends it - standing in for the signal an operator would send. A run that fails on the
// way up never gets as far as serving and reports that instead.
func runToCompletion(t *testing.T, cfg *config, c constructor, port uint16) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- run(ctx, logger.Test(t), "cron", cfg, c) }()

	deadline := time.After(30 * time.Second)
	for {
		select {
		case err := <-done:
			return err
		case <-deadline:
			cancel()
			t.Fatal("the run never started serving")
			return nil
		case <-time.After(5 * time.Millisecond):
			if serving(int(port)) {
				cancel()
				return <-done
			}
		}
	}
}

// TestRunRunsAndUnwinds is run's whole contract in one: it brings the process up, and a run that has
// finished has put back everything it changed.
//
// The port is what makes the unwind visible. A web server that was serving and is not any more is
// one that gave the port back, which nothing else in the run reports.
func TestRunRunsAndUnwinds(t *testing.T) {
	built := 0
	cfg, c, port := runArgs(t, func() *fake { built++; return newFake() })

	require.NoError(t, runToCompletion(t, cfg, c, port))
	assert.Equal(t, 1, built)

	assertPortFree(t, port)
}

// TestRunStartsTheCapability is what the root service buys: the capability is not merely built, it
// is running - which is what makes a health check mean anything.
func TestRunStartsTheCapability(t *testing.T) {
	f := newFake()
	cfg, c, port := runArgs(t, func() *fake { return f })

	require.NoError(t, runToCompletion(t, cfg, c, port))

	assert.True(t, f.started, "the run should have started the capability")
	assert.True(t, f.closed, "and closed it again on the way out")
}

// TestRunInstallsTelemetryBeforeBuildingTheCapability is the reason telemetry is started where it
// is, rather than with everything else the run supervises.
//
// A capability creates its instruments while it is being constructed - cron does, in NewMetrics -
// and an OTEL instrument resolves beholder.GetMeter() once, at creation. A capability built before
// this client became the process's would hold a noop meter for the life of the process, recording
// nothing, with no error anywhere to say so.
//
// Slow, and unavoidably so: it is the only test that builds a real beholder client, and closing one
// flushes to a collector that is not there - about 20s of export timeouts. The timeouts are on
// beholder.Config, which beholderConfig builds from the settings, so shortening them would mean a
// knob in production code that exists only for this.
func TestRunInstallsTelemetryBeforeBuildingTheCapability(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a real beholder client; ~20s of export timeouts on close")
	}

	cfg, _, port := runArgs(t, newFake)
	// Nothing is listening there, which is fine: the client is built and installed without dialling.
	cfg.observability.telemetry.Endpoint = fmt.Sprintf("localhost:%d", freePort(t))

	var duringBuild *beholder.Client
	c, err := newConstructor(func() *fake {
		duringBuild = beholder.GetClient()
		return newFake()
	})
	require.NoError(t, err)

	before := beholder.GetClient()
	require.NoError(t, runToCompletion(t, cfg, c, port))

	require.NotNil(t, duringBuild, "the constructor should have run")
	assert.NotSame(t, before, duringBuild, "the capability was built before telemetry was installed")
	assert.Same(t, before, beholder.GetClient(), "and the previous client should be back afterwards")
}

// TestRunUnwindsWhatStartedBeforeAFailure covers a constructor that fails after the observability
// services have been built: the run reports it and leaves nothing of itself behind.
//
// The beholder assertion below holds trivially here and is not evidence about telemetry. These
// settings configure no endpoint, so newTelemetry returns the noop service and installs nothing -
// there is nothing to put back. A run with telemetry configured would fail this if it asserted
// anything, which is the gap documented on the telemetry block in run: the global is swapped when
// telemetry is built, and the undo only works once the root has started it.
func TestRunUnwindsWhatStartedBeforeAFailure(t *testing.T) {
	before := beholder.GetClient()
	cfg, c, port := runArgs(t, func() (*fake, error) { return nil, errors.New("no schedule") })
	require.Empty(t, cfg.observability.telemetry.Endpoint, "these settings leave telemetry off")

	require.ErrorContains(t, runToCompletion(t, cfg, c, port), "no schedule")

	assert.Same(t, before, beholder.GetClient(), "nothing was installed, so nothing changed")
	// The web server never got as far as starting, so the port was never taken.
	assertPortFree(t, port)
}

// TestRunReportsWhereItFailed pins that a failure names the service that would not start, rather
// than arriving as something further in.
//
// It is also what proves run gets as far as the web server on the configured port at all.
func TestRunReportsWhereItFailed(t *testing.T) {
	cfg, c, port := runArgs(t, newFake)

	// Something else already has the port the web server is configured for.
	taken, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	require.ErrorContains(t, runToCompletion(t, cfg, c, port),
		fmt.Sprintf("failed to listen on port %d", port))
}

// TestRunStaysUpUntilItIsToldToStop is the whole point of the wait: a run keeps serving until
// something ends it, and only then unwinds.
//
// It is also the only test that looks at a run from outside while it is up: everything is still
// serving when the assertions below are made.
func TestRunStaysUpUntilItIsToldToStop(t *testing.T) {
	cfg, c, port := runArgs(t, newFake)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- run(ctx, logger.Test(t), "cron", cfg, c) }()

	require.Eventually(t, func() bool { return serving(int(port)) }, 30*time.Second, 5*time.Millisecond,
		"the run should be serving while it is up")

	// The root is started, and the health checker reports on the root - so a run that is up is a
	// run that says it is ready. This is what starting the capability bought.
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200} {
		res, err := http.Get(fmt.Sprintf("http://localhost:%d%s", port, path))
		require.NoError(t, err, path)
		require.NoError(t, res.Body.Close())
		assert.Equal(t, want, res.StatusCode, path)
	}

	// Still up: it has not unwound just because it finished starting.
	select {
	case err := <-done:
		t.Fatalf("the run ended on its own: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case err := <-done:
		// Being asked to stop is not a failure: a binary that exited because it was told to should
		// exit 0.
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not stop when it was told to")
	}

	assertPortFree(t, port)
}

// TestRunServesTheSettingsReloadEndpoint is the endpoint through the real run: the node dumps new
// settings to the file and hits /reload/settings.txt, and a 200 says every limit in the process now
// resolves against them.
//
// The handler reads the shared settings path - that path is the contract with the node - so this
// writes the real file and removes it again after: a leftover would be read by every other run in
// this package.
func TestRunServesTheSettingsReloadEndpoint(t *testing.T) {
	require.NoError(t, os.MkdirAll(filepath.Dir(settingsPath()), 0o700))
	require.NoError(t, os.WriteFile(settingsPath(), []byte("[global]\n"), 0o600))
	t.Cleanup(func() { _ = os.Remove(settingsPath()) })

	cfg, c, port := runArgs(t, newFake)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- run(ctx, logger.Test(t), "cron", cfg, c) }()

	require.Eventually(t, func() bool { return serving(int(port)) }, 30*time.Second, 5*time.Millisecond,
		"the run should be serving while it is up")

	res, err := http.Post(fmt.Sprintf("http://localhost:%d%s", port, reloadPath()), "", nil)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	assert.Equal(t, http.StatusOK, res.StatusCode)

	cancel()
	require.NoError(t, <-done)
}

// TestUnderPluginHost covers the cookie go-plugin's Serve insists on, which is what decides whether
// a run hands the process to a host or waits for a signal.
//
// The check is necessary rather than defensive: Serve exits the process when the cookie is absent,
// so a standalone binary that called it anyway would die on startup. The other branch is not tested
// here - plugin.Serve takes over the process's stdio and handshake, so a test calling it would be
// testing go-plugin rather than this.
func TestUnderPluginHost(t *testing.T) {
	h := loop.EmptyHandshakeConfig()

	t.Run("absent", func(t *testing.T) {
		t.Setenv(h.MagicCookieKey, "")
		assert.False(t, underPluginHost())
	})

	t.Run("wrong value", func(t *testing.T) {
		t.Setenv(h.MagicCookieKey, "not-the-cookie")
		assert.False(t, underPluginHost())
	})

	t.Run("present", func(t *testing.T) {
		t.Setenv(h.MagicCookieKey, h.MagicCookieValue)
		assert.True(t, underPluginHost())
	})
}

// assertPortFree reports whether nothing is listening on port, which is how a stopped web server is
// told from a running one.
func assertPortFree(t *testing.T, port uint16) {
	t.Helper()

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if assert.NoError(t, err, "port %d should be free", port) {
		require.NoError(t, l.Close())
	}
}
