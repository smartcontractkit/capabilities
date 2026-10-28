package capability

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// runArgs is the arguments of one run over settings a test can start: a real port, and everything
// else off.
func runArgs(t *testing.T) (*config, uint16) {
	t.Helper()

	port := uint16(freePort(t))
	cfg := defaultConfig("TEST_APP")
	cfg.grpc.Registerer = prometheus.NewRegistry()
	cfg.observability.http.Port = port
	// A stub the registrar's announcement can land on: registering the capability dials the
	// proxy, so it has to be a real listener rather than merely a lazy one.
	cfg.capabilities.ProxyURL = serveNodeRegistry(t, newSpyRegistry(t))

	return cfg, port
}

// testRunner returns a runner of one instance over cfg, with the logger of the test.
func testRunner(t *testing.T, cfg *config, c func(do.Injector) (Capability, error)) runner {
	return newRunner(logger.Test(t), "cron", cfg, c, NewInjector(), 1, cfg.capabilities, cfg.observability.http, nil)
}

// runToCompletion starts a run and stops it once it is serving, returning what run returned.
//
// A run blocks until its context is cancelled, so a test that wants one to finish has to be the
// thing that ends it - standing in for the signal an operator would send. A run that fails on the
// way up never gets as far as serving and reports that instead.
func runToCompletion(t *testing.T, cfg *config, c func(do.Injector) (Capability, error), port uint16) error {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- testRunner(t, cfg, c).run(ctx)
	}()

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
	cfg, port := runArgs(t)

	require.NoError(t, runToCompletion(t, cfg, func(do.Injector) (Capability, error) { built++; return newFake(), nil }, port))
	assert.Equal(t, 1, built)

	assertPortFree(t, port)
}

// TestRunStartsTheCapability is what the root service buys: the capability is not merely built, it
// is running - which is what makes a health check mean anything.
func TestRunStartsTheCapability(t *testing.T) {
	f := newFake()
	cfg, port := runArgs(t)

	require.NoError(t, runToCompletion(t, cfg, func(do.Injector) (Capability, error) { return f, nil }, port))

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
// beholder.Config, which telemetry.NewTelemetry builds from the settings, so shortening them would mean a
// knob in production code that exists only for this.
func TestRunInstallsTelemetryBeforeBuildingTheCapability(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a real beholder client; ~20s of export timeouts on close")
	}

	cfg, port := runArgs(t)
	// Nothing is listening there, which is fine: the client is built and installed without dialling.
	cfg.observability.TelemetryEndpoint = fmt.Sprintf("localhost:%d", freePort(t))
	cfg.observability.TelemetryEnabled = true

	var duringBuild *beholder.Client
	c := func(do.Injector) (Capability, error) {
		duringBuild = beholder.GetClient()
		return newFake(), nil
	}

	before := beholder.GetClient()
	// The run leaves its closed client installed; keep it from leaking into later tests.
	t.Cleanup(func() {
		beholder.SetClient(before)
		beholder.SetGlobalOtelProviders()
	})
	require.NoError(t, runToCompletion(t, cfg, c, port))

	require.NotNil(t, duringBuild, "the constructor should have run")
	assert.NotSame(t, before, duringBuild, "the capability was built before telemetry was installed")
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
	cfg, port := runArgs(t)
	c := func(do.Injector) (Capability, error) { return nil, errors.New("no schedule") }
	require.Empty(t, cfg.observability.TelemetryEndpoint, "these settings leave telemetry off")

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
	cfg, port := runArgs(t)

	// Something else already has the port the web server is configured for.
	taken, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)
	t.Cleanup(func() { _ = taken.Close() })

	require.ErrorContains(t, runToCompletion(t, cfg, fakeConstructor, port),
		fmt.Sprintf("failed to listen on port %d", port))
}

// TestRunStaysUpUntilItIsToldToStop is the whole point of the wait: a run keeps serving until
// something ends it, and only then unwinds.
//
// It is also the only test that looks at a run from outside while it is up: everything is still
// serving when the assertions below are made.
func TestRunStaysUpUntilItIsToldToStop(t *testing.T) {
	cfg, port := runArgs(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- testRunner(t, cfg, fakeConstructor).run(ctx)
	}()

	require.Eventually(t, func() bool { return serving(int(port)) }, 30*time.Second, 5*time.Millisecond,
		"the run should be serving while it is up")

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

type status [4]int

var probePaths = [4]string{"/livez", "/startupz", "/readyz", "/healthz"}

const (
	up   = http.StatusOK
	down = http.StatusServiceUnavailable
)

func assertProbes(t *testing.T, port uint16, when string, want status) {
	t.Helper()
	for i, path := range probePaths {
		assert.Equal(t, want[i], probe(t, port, path), "%s: %s", when, path)
	}
}

// TestRunProbeTransitions covers every combination of the capability's readiness and health across
// a run's lifecycle, from starting to stopped.
func TestRunProbeTransitions(t *testing.T) {
	cfg, port := runArgs(t)
	f := newControlledFake("ControlledCapability")
	f.setReady(errors.New("not connected"))
	f.setHealth(errors.New("not connected"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- testRunner(t, cfg, func(do.Injector) (Capability, error) { return f, nil }).run(ctx)
	}()

	// healthz is down because the run has not started, not because of the health set above.
	<-f.starting
	assertProbes(t, port, "starting", status{up, down, down, down})
	_, body := probeBody(t, port, "/startupz")
	assert.Contains(t, body, "Run: starting")

	close(f.releaseStart)
	require.Eventually(t, func() bool { return probe(t, port, "/startupz") == up }, 30*time.Second, 5*time.Millisecond,
		"the run should finish starting once the capability has")

	// Probes ask the capability on every request, so each change shows on the very next probe.
	assertProbes(t, port, "started, not ready or healthy", status{up, up, down, down})
	for _, path := range []string{"/readyz", "/healthz"} {
		_, body := probeBody(t, port, path)
		assert.Contains(t, body, "ControlledCapability: not connected", "%s should name the capability and why", path)
	}

	f.setReady(nil)
	assertProbes(t, port, "ready, not healthy", status{up, up, up, down})

	f.setReady(errors.New("busy"))
	f.setHealth(nil)
	assertProbes(t, port, "healthy, not ready", status{up, up, down, up})

	f.setReady(nil)
	assertProbes(t, port, "ready and healthy", status{up, up, up, up})
	for _, path := range probePaths {
		_, body := probeBody(t, port, path)
		assert.Equal(t, "ok\n", body, path)
	}

	f.setHealth(errors.New("lost connection"))
	assertProbes(t, port, "unhealthy again", status{up, up, up, down})

	f.setReady(errors.New("lost connection"))
	assertProbes(t, port, "not ready again", status{up, up, down, down})

	// The capability is ready and healthy, so the probes going down while stopping is the run's doing.
	f.setReady(nil)
	f.setHealth(nil)
	assertProbes(t, port, "back up before stopping", status{up, up, up, up})
	cancel()
	<-f.closing
	assertProbes(t, port, "stopping", status{up, up, down, down})
	_, body = probeBody(t, port, "/readyz")
	assert.Contains(t, body, "Run: shutting down")

	close(f.releaseClose)
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not stop when it was told to")
	}
	assertPortFree(t, port)
}

func TestRunProbesWhenTheCapabilityFailsToStart(t *testing.T) {
	cfg, port := runArgs(t)
	f := newControlledFake("ControlledCapability")
	f.startErr = errors.New("no schedule")

	done := make(chan error, 1)
	go func() {
		done <- testRunner(t, cfg, func(do.Injector) (Capability, error) { return f, nil }).run(context.Background())
	}()

	<-f.starting
	assertProbes(t, port, "failing to start", status{up, down, down, down})

	close(f.releaseStart)
	select {
	case err := <-done:
		require.ErrorContains(t, err, "no schedule")
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not end when its capability failed to start")
	}
	assertPortFree(t, port)
}

func TestRunProbesAcrossInstances(t *testing.T) {
	cfg, port := runArgs(t)
	cfg.capabilities.ProxyURL = ""
	fakes := []*controlledFake{newControlledFake("Instance0"), newControlledFake("Instance1")}
	for _, f := range fakes {
		close(f.releaseStart)
	}
	fakes[1].setReady(errors.New("not connected"))
	fakes[1].setHealth(errors.New("not connected"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- newRunner(logger.Test(t), "cron", cfg, func(i do.Injector) (Capability, error) {
			return fakes[do.MustInvoke[InstanceInfo](i).ID], nil
		}, NewInjector(), len(fakes), cfg.capabilities, cfg.observability.http, nil).run(ctx)
	}()
	require.Eventually(t, func() bool { return serving(int(port)) }, 30*time.Second, 5*time.Millisecond,
		"the run should finish starting")

	assertProbes(t, port, "one instance not online", status{up, up, down, down})
	for _, path := range []string{"/readyz", "/healthz"} {
		_, body := probeBody(t, port, path)
		assert.Contains(t, body, "Instance1: not connected", path)
		assert.NotContains(t, body, "Instance0:", path)
	}

	fakes[1].setReady(nil)
	fakes[1].setHealth(nil)
	assertProbes(t, port, "every instance online", status{up, up, up, up})

	fakes[0].setHealth(errors.New("lost connection"))
	assertProbes(t, port, "the other instance unhealthy", status{up, up, up, down})

	for _, f := range fakes {
		close(f.releaseClose)
	}
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the run did not stop when it was told to")
	}
}

type controlledFake struct {
	*fake

	starting, releaseStart chan struct{}
	closing, releaseClose  chan struct{}
	startErr               error

	ready, health atomic.Pointer[error]
}

func newControlledFake(name string) *controlledFake {
	f := &controlledFake{
		fake:         &fake{},
		starting:     make(chan struct{}),
		releaseStart: make(chan struct{}),
		closing:      make(chan struct{}),
		releaseClose: make(chan struct{}),
	}
	f.setReady(nil)
	f.setHealth(nil)
	f.info, _ = capabilities.NewCapabilityInfo(fakeID, capabilities.CapabilityTypeCombined, "a fake")
	f.Service, _ = services.Config{
		Name:  name,
		Start: func(context.Context) error { close(f.starting); <-f.releaseStart; return f.startErr },
		Close: func() error { close(f.closing); <-f.releaseClose; return nil },
	}.NewServiceEngine(logger.Nop())
	return f
}

func (f *controlledFake) setReady(err error) { f.ready.Store(&err) }

func (f *controlledFake) setHealth(err error) { f.health.Store(&err) }

func (f *controlledFake) Ready() error {
	if err := f.Service.Ready(); err != nil {
		return err
	}
	return *f.ready.Load()
}

func (f *controlledFake) HealthReport() map[string]error {
	if err := f.Service.Ready(); err != nil {
		return map[string]error{f.Name(): err}
	}
	return map[string]error{f.Name(): *f.health.Load()}
}

func probe(t *testing.T, port uint16, path string) int {
	t.Helper()

	status, _ := probeBody(t, port, path)
	return status
}

func probeBody(t *testing.T, port uint16, path string) (int, string) {
	t.Helper()

	res, err := httpGet(t.Context(), fmt.Sprintf("http://localhost:%d%s", port, path))
	require.NoError(t, err, path)
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err, path)
	require.NoError(t, res.Body.Close())
	return res.StatusCode, string(body)
}

// TestUnderPluginHost covers the cookie go-plugin's Serve insists on, which is what decides whether
// a run hands the process to a host or waits for a signal.
//
// The check is necessary rather than defensive: Serve exits the process when the cookie is absent,
// so a standalone binary that called it anyway would die on startup. The other branch is not tested
// here - plugin.Serve takes over the process's stdio and handshake, so a test calling it would be
// testing go-plugin rather than this.
func TestUnderPluginHost(t *testing.T) {
	h := emptyHandshakeConfig()

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
func assertPortFree[P int | uint16](t *testing.T, port P) {
	t.Helper()

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if assert.NoError(t, err, "port %d should be free", port) {
		require.NoError(t, l.Close())
	}
}

func TestStopInReverseStopsInTheReverseOfTheStartOrder(t *testing.T) {
	var stopped []string
	named := func(name string) services.Service {
		s, _ := services.Config{Name: name, Close: func() error { stopped = append(stopped, name); return nil }}.NewServiceEngine(logger.Nop())
		require.NoError(t, s.Start(t.Context()))
		return s
	}

	stopInReverse(logger.Test(t), []services.Service{named("Telemetry"), named("Capability"), named("Web")})

	assert.Equal(t, []string{"Web", "Capability", "Telemetry"}, stopped)
}
