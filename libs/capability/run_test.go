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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
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
func execute(t *testing.T, lggr logger.Logger, ctor any, args ...string) error {
	t.Helper()

	// A free port rather than the default, since tests share a machine and run alongside each
	// other - two of them on 8080 would collide. It is also how this tells that the binary is up.
	port := 0
	if slices.Contains(args, "run") && !slices.Contains(args, "--http.port") {
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
			if serving(port) {
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

// freePort is a port nothing is listening on, found by listening on one and stopping again.
func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())
	return port
}

func TestRunErrRejects(t *testing.T) {
	lggr := logger.Test(t)

	t.Run("no logger", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), nil, newFake), "must provide a logger")
	})

	t.Run("something that is not a function", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), lggr, "cron"), "must be a function, got string")
	})

	t.Run("nothing at all", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), lggr, nil), "must be a function")
	})

	t.Run("a function returning something that cannot be hosted", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), lggr, func() string { return "" }),
			"string does not implement capability.Capability")
	})

	t.Run("a function returning nothing", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), lggr, func() {}), "must return the capability")
	})

	t.Run("a function whose second result is not an error", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), lggr, func() (*fake, string) { return nil, "" }),
			"must return an error second")
	})
}

func TestRunBuildsTheCapability(t *testing.T) {
	built := 0

	require.NoError(t, execute(t, logger.Test(t), func() *fake { built++; return newFake() }, "run"))
	assert.Equal(t, 1, built, "the constructor should have been called exactly once")
}

func TestRunReportsAFailedConstructor(t *testing.T) {
	err := execute(t, logger.Test(t), func() (*fake, error) { return nil, errors.New("no schedule") }, "run")
	require.ErrorContains(t, err, "failed to build the capability: no schedule")
}

// TestRunNamesWhatItCannotProvide covers a parameter nothing answers: it is reported before
// anything starts, and names the type that went unmatched rather than the position it was in.
func TestRunNamesWhatItCannotProvide(t *testing.T) {
	err := execute(t, logger.Test(t), func(string) *fake { return newFake() }, "run")
	require.ErrorContains(t, err, "asks for a string, and nothing in this run provides one")
}

// TestRunPassesTheLoggerToTheConstructor covers the first dependency: this process's logger, so a
// capability names its own from it rather than building one the operator cannot configure.
func TestRunPassesTheLoggerToTheConstructor(t *testing.T) {
	var got logger.Logger

	require.NoError(t, execute(t, logger.Test(t), func(l logger.Logger) *fake {
		got = l
		return newFake()
	}, "run"))

	assert.NotNil(t, got, "the constructor should have been handed the run's logger")
}

// TestRunPassesTheRegistryToTheConstructor covers the registry a capability resolves other
// capabilities through.
//
// By type rather than by position, so a constructor asks for what it needs and takes nothing it
// does not - which is what lets the one below coexist with the no-argument constructors elsewhere
// in these tests.
func TestRunPassesTheRegistryToTheConstructor(t *testing.T) {
	var got registry.Registry

	require.NoError(t, execute(t, logger.Test(t), func(r registry.Registry) *fake {
		got = r
		return newFake()
	}, "run"))

	assert.NotNil(t, got, "the constructor should have been handed the run's registry")
}

// TestRunPassesTheLimitsFactoryToTheConstructor covers the second dependency: what a capability
// bounds a workflow's requests with.
//
// A struct rather than an interface, which the type matching handles the same way - what a
// constructor asks for is a type, not a kind of type.
func TestRunPassesTheLimitsFactoryToTheConstructor(t *testing.T) {
	var got limits.Factory

	require.NoError(t, execute(t, logger.Test(t), func(f limits.Factory) *fake {
		got = f
		return newFake()
	}, "run"))

	assert.NotNil(t, got.Settings, "the factory should be built over this run's settings")
	assert.NotNil(t, got.Meter)
}

// TestRunPassesEveryDependencyItHas covers a constructor asking for more than one, in an order of
// its own: matching is by type, so the order it lists them in is its business.
func TestRunPassesEveryDependencyItHas(t *testing.T) {
	var (
		gotLimits   limits.Factory
		gotRegistry core.CapabilitiesRegistry
	)

	require.NoError(t, execute(t, logger.Test(t), func(f limits.Factory, r core.CapabilitiesRegistry) *fake {
		gotLimits, gotRegistry = f, r
		return newFake()
	}, "run"))

	assert.NotNil(t, gotLimits.Settings)
	assert.NotNil(t, gotRegistry)
}

// testConfig is a capability's own config, declared by embedding Config: the one parameter an
// operator supplies rather than the run resolves.
type testConfig struct {
	Config
	Shout string `usage:"what the fake says"`
}

// TestRunHandsTheConstructorItsConfig covers the parameter that is not a dependency: the
// capability's own settings, bound under the binary's name and handed over decoded. The binary is
// named cron - execute's argv[0] - so its flag is --cron.shout.
func TestRunHandsTheConstructorItsConfig(t *testing.T) {
	var got testConfig

	require.NoError(t, execute(t, logger.Test(t), func(cfg testConfig) *fake {
		got = cfg
		return newFake()
	}, "run", "--cron.shout", "hello"))

	assert.Equal(t, "hello", got.Shout)
}

// TestRunReadsTheCapabilityConfigFromTheEnvironment is the same binding from the other direction:
// CRE_, then the binary's name, as every other section's settings are.
func TestRunReadsTheCapabilityConfigFromTheEnvironment(t *testing.T) {
	t.Setenv("CRE_CRON_SHOUT", "from the environment")

	var got testConfig

	require.NoError(t, execute(t, logger.Test(t), func(cfg testConfig) *fake {
		got = cfg
		return newFake()
	}, "run"))

	assert.Equal(t, "from the environment", got.Shout)
}

// TestRunHandsTheConstructorItsConfigAndItsDependencies covers the two kinds of parameter side by
// side: the config from the flags, the dependencies from the run, and neither answered by the
// other.
func TestRunHandsTheConstructorItsConfigAndItsDependencies(t *testing.T) {
	var (
		gotCfg    testConfig
		gotLimits limits.Factory
	)

	require.NoError(t, execute(t, logger.Test(t), func(cfg testConfig, f limits.Factory) *fake {
		gotCfg, gotLimits = cfg, f
		return newFake()
	}, "run", "--cron.shout", "hello"))

	assert.Equal(t, "hello", gotCfg.Shout)
	assert.NotNil(t, gotLimits.Settings)
}

// TestRunRejectsTwoConfigs pins the one-config rule: the struct is bound under the binary's name,
// so two of them would share one namespace and which setting belonged to which would be anyone's
// guess.
func TestRunRejectsTwoConfigs(t *testing.T) {
	err := execute(t, logger.Test(t), func(a, b testConfig) *fake { return newFake() }, "run")
	require.ErrorContains(t, err, "takes its config once")
}

// TestRunRejectsTheDependenciesStruct pins the rule the other way round: a constructor declares the
// things it uses, not the bag they came in.
//
// Taking the struct would be a dependency on everything a run has, and adding a field to it would
// silently widen what every capability appeared to need.
func TestRunRejectsTheDependenciesStruct(t *testing.T) {
	err := execute(t, logger.Test(t), func(Dependencies) *fake { return newFake() }, "run")

	require.ErrorContains(t, err, "asks for a capability.Dependencies, and nothing in this run provides one")
}

// TestRunPassesTheRegistryAsANarrowerInterface covers matching by assignability: a capability that
// only needs to resolve capabilities can say so, without naming the wider type the run holds.
func TestRunPassesTheRegistryAsANarrowerInterface(t *testing.T) {
	var got core.CapabilitiesRegistry

	require.NoError(t, execute(t, logger.Test(t), func(r core.CapabilitiesRegistry) *fake {
		got = r
		return newFake()
	}, "run"))

	assert.NotNil(t, got)
}

// TestRunHasARunCommand is the shape of the binary rather than what it does: a root that runs
// nothing itself, and a way to start it hanging off it.
func TestRunHasARunCommand(t *testing.T) {
	// A bare invocation prints help and does nothing, rather than starting the capability.
	require.NoError(t, execute(t, logger.Test(t), func() *fake {
		t.Error("the root command should not build the capability")
		return newFake()
	}))
}

// TestRunServesAndAnnouncesTheCapability is the whole path through the real entry point: a run
// serves its capability, tells the node's registry where, and takes it back on shutdown.
//
// The removal half is reliable to assert here because the registry's close is one ordered
// function: the Remove RPC is sent before the connection it travels on is closed.
func TestRunServesAndAnnouncesTheCapability(t *testing.T) {
	stub := &stubRegistry{adds: map[string]string{}}
	proxyURL := serveStubRegistry(t, stub)

	require.NoError(t, execute(t, logger.Test(t), func() *fake { return newFake() },
		"run", "--capabilities.proxy-url", proxyURL))

	addr, ok := stub.announced(fakeID)
	require.True(t, ok, "the run should have announced the capability to the node's registry")
	assert.NotEmpty(t, addr)

	stub.mu.Lock()
	defer stub.mu.Unlock()
	assert.Contains(t, stub.removes, fakeID, "and deregistered it on the way out")
}
