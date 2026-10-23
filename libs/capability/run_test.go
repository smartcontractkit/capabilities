package capability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/do/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

// fake stands in for a generated capability server, which is what a real constructor returns. Only
// its type matters here: nothing calls it, because building it is as far as this step goes.
//
// The interfaces are embedded rather than implemented so that this says what a capability is - a
// service and a capability - without a page of methods saying it.
type fake struct {
	services.Service
	capabilities.ExecutableAndTriggerCapability

	// info is what the fake reports itself as, which is what the registrar serves and announces
	// it by.
	info capabilities.CapabilityInfo

	// started and closed record what the root service did with it. Written on the run's goroutine
	// and read on the test's, but only after the run has returned, so the channel that reports that
	// is what orders them.
	started bool
	closed  bool
}

func (f *fake) Info(context.Context) (capabilities.CapabilityInfo, error) { return f.info, nil }

// fakeID is what the fake registers and announces itself as.
const fakeID = "fake@1.0.0"

// newFake builds one with a real service behind it, since the health checker asks the capability
// its name and whether it is ready.
func newFake() *fake { return newFakeNamed(logger.Nop()) }

// newFakeNamed is a fake whose service is named from lggr, as a real capability's is from the logger
// it is given.
func newFakeNamed(lggr logger.Logger) *fake {
	f := &fake{}
	f.info, _ = capabilities.NewCapabilityInfo(fakeID, capabilities.CapabilityTypeCombined, "a fake")
	f.Service, _ = services.Config{
		Name:  "FakeCapability",
		Start: func(context.Context) error { f.started = true; return nil },
		Close: func() error { f.closed = true; return nil },
	}.NewServiceEngine(lggr)
	return f
}

// execute drives the real entry point with args of its own, and stops it once it is up.
//
// os.Args rather than a seam into the command tree, because that is what RunErr reads and what a
// binary is actually started with - including argv[0], which is what names the root command.
//
// A run blocks until its context is cancelled, so this waits until the binary is serving and then
// cancels - which is a test standing in for the signal an operator would send. A run that fails on
// the way up never gets there and reports that instead.
func execute(t *testing.T, lggr logger.Logger, ctor func(do.Injector) (Capability, error), args ...string) error {
	t.Helper()

	// A free port rather than the default, since tests share a machine and run alongside each
	// other - two of them on 8080 would collide. It is also how this tells that the binary is up.
	port := 0
	if (slices.Contains(args, "run") || slices.Contains(args, "embed")) && !slices.Contains(args, "--http.port") {
		port = freePort(t)
		args = append(args, "--http.port", strconv.Itoa(port))

		// No --capabilities.proxy-url: it is optional, and leaving it out is the simpler run - the
		// registry then resolves only what this binary hosts, and dials nothing.
	}

	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	os.Args = append([]string{"cron"}, args...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- RunErr(ctx, ctor, withLogger(lggr), withRegisterer(prometheus.NewRegistry())) }()

	if port == 0 {
		// Nothing that serves: a rejected constructor, or a bare invocation that prints help.
		return <-done
	}

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
			if serving(port) {
				cancel()
				return <-done
			}
		}
	}
}

// serving checks /startupz because the web server answers before the run has finished starting.
//
// A timeout, because the port is not always the run's: a test that takes the port to watch the
// run fail leaves a listener that accepts and never answers, and a bare Get would hang on it
// rather than report not-serving.
func serving(port int) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := httpGet(ctx, fmt.Sprintf("http://localhost:%d/startupz", port))
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

// freePort is a port nothing is listening on, found by listening on one and stopping again.
func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

// fakeConstructor builds a fake and asks the injector for nothing.
func fakeConstructor(do.Injector) (Capability, error) { return newFake(), nil }

func TestRunErrRejects(t *testing.T) {
	t.Run("no constructor", func(t *testing.T) {
		require.ErrorContains(t, RunErr[Capability](context.Background(), nil, withLogger(logger.Test(t))), "must provide a capability constructor")
	})
}

func TestRunBuildsTheCapability(t *testing.T) {
	built := 0

	require.NoError(t, execute(t, logger.Test(t), func(do.Injector) (Capability, error) {
		built++
		return newFake(), nil
	}, "run"))
	assert.Equal(t, 1, built, "the constructor should have been called exactly once")
}

func TestRunReportsAFailedConstructor(t *testing.T) {
	err := execute(t, logger.Test(t), func(do.Injector) (Capability, error) {
		return nil, errors.New("no schedule")
	}, "run")
	require.ErrorContains(t, err, "failed to instantiate capability: no schedule")
}

// TestRunRejectsANilCapability covers a constructor that reports success without building
// anything: the run says so rather than starting a nil capability.
func TestRunRejectsANilCapability(t *testing.T) {
	err := execute(t, logger.Test(t), func(do.Injector) (Capability, error) { return nil, nil }, "run")
	require.ErrorContains(t, err, "returned neither a capability nor an error")
}

// TestRunProvidesItsDependencies covers what the injector holds: this process's logger, the
// registry capabilities are resolved through, the factory a workflow's requests are bounded by, and
// the clock.
func TestRunProvidesItsDependencies(t *testing.T) {
	var (
		gotLogger   logger.Logger
		gotRegistry registry.CapabilitiesRegistry
		gotLimits   limits.Factory
		gotClock    clockwork.Clock
	)

	require.NoError(t, execute(t, logger.Test(t), func(i do.Injector) (Capability, error) {
		gotLogger = do.MustInvoke[logger.Logger](i)
		gotRegistry = do.MustInvoke[registry.CapabilitiesRegistry](i)
		gotLimits = do.MustInvoke[limits.Factory](i)
		gotClock = do.MustInvoke[clockwork.Clock](i)
		return newFake(), nil
	}, "run"))

	assert.NotNil(t, gotLogger)
	assert.NotNil(t, gotRegistry)
	assert.NotNil(t, gotLimits.Settings)
	assert.NotNil(t, gotLimits.Meter)
	assert.NotNil(t, gotClock)
}

// TestRunHasARunCommand is the shape of the binary rather than what it does: a root that runs
// nothing itself, and a way to start it hanging off it.
func TestRunHasARunCommand(t *testing.T) {
	// A bare invocation prints help and does nothing, rather than starting the capability.
	require.NoError(t, execute(t, logger.Test(t), func(do.Injector) (Capability, error) {
		t.Error("the root command should not build the capability")
		return newFake(), nil
	}))
}

// TestRunServesAndAnnouncesTheCapability is the whole path through the real entry point: a run
// serves its capability, tells the node's registry where, and takes it back on shutdown.
//
// The removal half is reliable to assert here because the registry's close is one ordered
// function: the Remove RPC is sent before the connection it travels on is closed.
func TestRunServesAndAnnouncesTheCapability(t *testing.T) {
	node := newSpyRegistry(t)
	proxyURL := serveNodeRegistry(t, node)

	require.NoError(t, execute(t, logger.Test(t), fakeConstructor, "run", "--capabilities.proxy-url", proxyURL))

	assert.Equal(t, []string{fakeID}, node.addedIDs(), "the run should have announced the capability to the node's registry")
	assert.Equal(t, []string{fakeID}, node.removedIDs(), "and deregistered it on the way out")
}

// TestRunInjectorConfigIsOnlyOnTheRunCommand covers which command each injector's configs are registered on.
func TestRunInjectorConfigIsOnlyOnTheRunCommand(t *testing.T) {
	type runOnly struct{ Name string }
	conf := &runOnly{}

	runInjector := RunInjector
	t.Cleanup(func() { RunInjector = runInjector })
	RunInjector = runInjector.Clone()
	require.NoError(t, RunInjector.ProvideConfig("runonly", conf))

	require.NoError(t, execute(t, logger.Test(t), fakeConstructor, "run", "--runonly.name", "set"))
	assert.Equal(t, "set", conf.Name)

	require.ErrorContains(t, execute(t, logger.Test(t), fakeConstructor, "embed", "1", "--runonly.name", "set"),
		"unknown flag: --runonly.name")
}
