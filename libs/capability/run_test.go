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
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// fake stands in for a generated capability server, which is what a real constructor returns. Only
// its type matters here: nothing calls it, because building it is as far as this step goes.
//
// The interfaces are embedded rather than implemented so that this says what a capability is - a
// service, a capability, and the proto service behind it - without a page of methods saying it.
type fake struct {
	runnable
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

// runnable holds the services.Service half. It is a type of its own so that the field it embeds is
// not called Service, which would collide with the method below.
type runnable struct{ services.Service }

// Service is the base capability's descriptor rather than nil: the debug UI builds its page from
// what this returns and refuses a capability without one.
func (fake) Service() protoreflect.ServiceDescriptor {
	return capabilitiespb.File_capabilities_proto.Services().ByName("BaseCapability")
}

func (f *fake) Info(context.Context) (capabilities.CapabilityInfo, error) { return f.info, nil }

// fakeID is what the fake registers and announces itself as.
const fakeID = "fake@1.0.0"

// newFake builds one with a real service behind it, since the health checker asks the capability
// its name and whether it is ready.
func newFake() *fake {
	f := &fake{}
	f.info, _ = capabilities.NewCapabilityInfo(fakeID, capabilities.CapabilityTypeCombined, "a fake")
	f.runnable.Service, _ = services.Config{
		Name:  "FakeCapability",
		Start: func(context.Context) error { f.started = true; return nil },
		Close: func() error { f.closed = true; return nil },
	}.NewServiceEngine(logger.Nop())
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
	ctor, registered := whenRegistered(ctor)

	// A free port rather than the default, since tests share a machine and run alongside each
	// other - two of them on 8080 would collide. It is also how this tells that the binary is up.
	port := 0
	if slices.Contains(args, "run") && !slices.Contains(args, "--http.port") {
		port = freePort(t)
		args = append(args, "--http.port", strconv.Itoa(port))

		// No --capabilities.proxy-url: it is optional, and leaving it out is the simpler run - the
		// registry then resolves only what this binary hosts, and dials nothing.
	}

	isolateLoopServer(t)

	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	os.Args = append([]string{"cron"}, args...)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- RunErr(ctx, lggr, ctor) }()

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
			if serving(port) && registered() {
				cancel()
				return <-done
			}
		}
	}
}

// serving reports whether the shared HTTP server is answering on port, which is how a test tells
// that a run has finished coming up.
//
// A timeout, because the port is not always the run's: a test that takes the port to watch the
// run fail leaves a listener that accepts and never answers, and a bare Get would hang on it
// rather than report not-serving.
func serving(port int) bool {
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(fmt.Sprintf("http://localhost:%d/metrics", port))
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// whenRegistered wraps ctor so that registered reports when its capability has joined the health
// checker, which a run does last, once the capability is announced.
//
// Serving alone does not say a run is up: loop.Server's web server is the first thing it starts
// rather than the last, so a run cancelled as soon as it serves can be cut off mid-announcement.
func whenRegistered(ctor func(do.Injector) (Capability, error)) (func(do.Injector) (Capability, error), func() bool) {
	var reported atomic.Bool
	wrapped := func(i do.Injector) (Capability, error) {
		c, err := ctor(i)
		if c == nil || err != nil {
			return c, err
		}
		return &reportsRegistration{Capability: c, reported: &reported}, nil
	}
	return wrapped, reported.Load
}

type reportsRegistration struct {
	Capability
	reported *atomic.Bool
}

func (r *reportsRegistration) HealthReport() map[string]error {
	r.reported.Store(true)
	return r.Capability.HealthReport()
}

// isolateLoopServer gives a run fresh copies of the process globals loop.Server registers on, which
// it can only do once per process: /metrics on http.DefaultServeMux, and its gRPC metrics on
// prometheus.DefaultRegisterer. A second run in the same test binary would otherwise panic.
//
// The fresh mux has no /debug/pprof, which net/http/pprof registers on the original at init.
func isolateLoopServer(t *testing.T) {
	t.Helper()

	mux, registerer, gatherer := http.DefaultServeMux, prometheus.DefaultRegisterer, prometheus.DefaultGatherer
	t.Cleanup(func() {
		http.DefaultServeMux, prometheus.DefaultRegisterer, prometheus.DefaultGatherer = mux, registerer, gatherer
	})

	registry := prometheus.NewRegistry()
	http.DefaultServeMux, prometheus.DefaultRegisterer, prometheus.DefaultGatherer = http.NewServeMux(), registry, registry
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
	t.Run("no logger", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), nil, fakeConstructor), "must provide a logger")
	})

	t.Run("no constructor", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), logger.Test(t), nil), "must provide a capability constructor")
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

// TestRunIgnoresTheDatabaseInItsEnv covers the env the shim leaves alone: loop.Server would read a
// CL_DATABASE_URL already set and connect to it, which this config never asked for.
func TestRunIgnoresTheDatabaseInItsEnv(t *testing.T) {
	t.Setenv("CL_DATABASE_URL", "postgres://nobody@localhost:1/none")

	require.NoError(t, execute(t, logger.Test(t), fakeConstructor, "run"))
}

// TestRunProvidesItsDependencies covers what the injector holds: this process's logger, the
// registry capabilities are resolved through, and the factory a workflow's requests are bounded by.
func TestRunProvidesItsDependencies(t *testing.T) {
	var (
		gotLogger   logger.Logger
		gotRegistry core.CapabilitiesRegistry
		gotLimits   limits.Factory
	)

	require.NoError(t, execute(t, logger.Test(t), func(i do.Injector) (Capability, error) {
		gotLogger = do.MustInvoke[logger.Logger](i)
		gotRegistry = do.MustInvoke[core.CapabilitiesRegistry](i)
		gotLimits = do.MustInvoke[limits.Factory](i)
		return newFake(), nil
	}, "run"))

	assert.NotNil(t, gotLogger)
	assert.NotNil(t, gotRegistry)
	assert.NotNil(t, gotLimits.Settings)
	assert.NotNil(t, gotLimits.Meter)
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
