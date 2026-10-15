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
	"sync"
	"testing"
	"time"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/services/orgresolver"
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
func newFake() *fake { return newFakeNamed("FakeCapability") }

func newFakeNamed(name string) *fake {
	f := &fake{}
	f.info, _ = capabilities.NewCapabilityInfo(fakeID, capabilities.CapabilityTypeCombined, "a fake")
	f.runnable.Service, _ = services.Config{
		Name:  name,
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
func execute(t *testing.T, lggr logger.Logger, a app, opts []Option, args ...string) error {
	t.Helper()

	resetRegistrations(t)
	if a.setup != nil {
		a.setup()
	}

	// A free port rather than the default, since tests share a machine and run alongside each
	// other - two of them on 8080 would collide. It is also how this tells that the binary is up.
	port := 0
	serves := slices.Contains(args, "run") || slices.Contains(args, "embed")
	if serves && !slices.Contains(args, "--http.port") {
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
	go func() { done <- a.runErr(ctx, lggr, opts...) }()

	if port == 0 {
		// Nothing that serves: a rejected registration, or a bare invocation that prints help.
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

// app is what a binary brings to a run: what its packages register from init, the constructor
// it hands Run, and its own config.
type app struct {
	setup func()
	ctor  Constructor
	// runErr is RunErr over ctor and the app's ConfigProvider, kept as a closure because the
	// provider's type parameter is the app's own. Nil runs with no config of its own.
	run func(ctx context.Context, lggr logger.Logger, ctor Constructor, opts ...Option) error
}

// runErr is RunErr as the app's binary calls it.
func (a app) runErr(ctx context.Context, lggr logger.Logger, opts ...Option) error {
	if a.run != nil {
		return a.run(ctx, lggr, a.ctor, opts...)
	}
	return RunErr(ctx, lggr, a.ctor, NewConfigProvider(struct{}{}), opts...)
}

// withConfig is a, with cfg as its own config.
func withConfig[T any](a app, cfg ConfigProvider[T]) app {
	a.run = func(ctx context.Context, lggr logger.Logger, ctor Constructor, opts ...Option) error {
		return RunErr(ctx, lggr, ctor, cfg, opts...)
	}
	return a
}

// resetRegistrations gives one test fresh, empty registrations, and puts the process's back
// afterwards: a run registers its settings, so two tests sharing them would collide. RunErr
// registers the run's settings itself.
func resetRegistrations(t *testing.T) {
	t.Helper()

	run, embed := runRegistrations, embedRegistrations
	t.Cleanup(func() { runRegistrations, embedRegistrations = run, embed })

	runRegistrations, embedRegistrations = newRegistrations(), newRegistrations()
}

// resetWithSettings is resetRegistrations with the run's settings registered, as RunErr would, for
// a test that does not go through RunErr.
func resetWithSettings(t *testing.T) {
	t.Helper()

	resetRegistrations(t)
	registerSettings()
}

// runConfig is the config registered for run under namespace, as the *T its flags decode into, so
// a test can set it as an operator would.
func runConfig[T any](t *testing.T, namespace string) *T {
	t.Helper()

	cfg, ok := runRegistrations.configs[namespace].value.(*T)
	require.True(t, ok, "no %T registered under %q", cfg, namespace)
	return cfg
}

// provides is an app of one capability, built by build from its instance's container.
func provides(build func(do.Injector) (*fake, error)) app {
	return app{ctor: func(c do.Injector) (Capability, error) {
		f, err := build(c)
		if err != nil {
			return nil, err
		}
		return f, nil
	}}
}

// providesFake is provides for a capability that asks for nothing.
func providesFake(build func() *fake) app {
	return provides(func(do.Injector) (*fake, error) { return build(), nil })
}

// withSetup is a, with setup standing in for its packages' init.
func (a app) withSetup(setup func()) app {
	a.setup = setup
	return a
}

func TestRunErrRejects(t *testing.T) {
	lggr := logger.Test(t)

	t.Run("no logger", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), nil, providesFake(newFake).ctor, NewConfigProvider(struct{}{})),
			"must provide a logger")
	})

	t.Run("no constructor", func(t *testing.T) {
		require.ErrorContains(t, RunErr(context.Background(), lggr, nil, NewConfigProvider(struct{}{})),
			"must provide a capability constructor")
	})
}

func TestRunBuildsTheCapability(t *testing.T) {
	built := 0

	require.NoError(t, execute(t, logger.Test(t), providesFake(func() *fake { built++; return newFake() }), nil, "run"))
	assert.Equal(t, 1, built, "the capability should have been built exactly once")
}

func TestRunReportsAFailedConstructor(t *testing.T) {
	err := execute(t, logger.Test(t), provides(func(do.Injector) (*fake, error) {
		return nil, errors.New("no schedule")
	}), nil, "run")
	require.ErrorContains(t, err, "failed to build the capability")
	require.ErrorContains(t, err, "no schedule")
}

// TestRunReportsANoCapability covers a constructor that returns neither a capability nor an error.
func TestRunReportsANoCapability(t *testing.T) {
	err := execute(t, logger.Test(t), app{ctor: func(do.Injector) (Capability, error) {
		return nil, nil
	}}, nil, "run")
	require.ErrorContains(t, err, "the constructor returned none")
}

// TestRunNamesWhatItCannotProvide covers a dependency nothing answers: it is reported before
// anything starts, and names the type that went unmatched.
func TestRunNamesWhatItCannotProvide(t *testing.T) {
	err := execute(t, logger.Test(t), provides(func(r do.Injector) (*fake, error) {
		if _, err := do.Invoke[string](r); err != nil {
			return nil, err
		}
		return newFake(), nil
	}), nil, "run")
	require.ErrorContains(t, err, "string")
}

// TestRunPassesTheLoggerToTheConstructor covers the first dependency: this process's logger, so a
// capability names its own from it rather than building one the operator cannot configure.
func TestRunPassesTheLoggerToTheConstructor(t *testing.T) {
	var got logger.Logger

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (f *fake, err error) {
		got, err = do.Invoke[logger.Logger](r)
		return newFake(), err
	}), nil, "run"))

	assert.NotNil(t, got, "the capability should have been handed the run's logger")
}

// TestRunPassesTheRegistryToTheConstructor covers the registry a capability resolves other
// capabilities through.
func TestRunPassesTheRegistryToTheConstructor(t *testing.T) {
	var got registry.CapabilitiesRegistry

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (f *fake, err error) {
		got, err = do.Invoke[registry.CapabilitiesRegistry](r)
		return newFake(), err
	}), nil, "run"))

	assert.NotNil(t, got, "the capability should have been handed the run's registry")
}

// TestRunPassesTheRegistryAsTheCoreInterface covers core.CapabilitiesRegistry, the name most
// capabilities know the registry by: an alias of registry.CapabilitiesRegistry, so it resolves.
func TestRunPassesTheRegistryAsTheCoreInterface(t *testing.T) {
	var got core.CapabilitiesRegistry

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (f *fake, err error) {
		got, err = do.Invoke[core.CapabilitiesRegistry](r)
		return newFake(), err
	}), nil, "run"))

	assert.NotNil(t, got)
}

// TestRunPassesTheLimitsFactoryToTheConstructor covers what a capability bounds a workflow's
// requests with.
func TestRunPassesTheLimitsFactoryToTheConstructor(t *testing.T) {
	var got limits.Factory

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (f *fake, err error) {
		got, err = do.Invoke[limits.Factory](r)
		return newFake(), err
	}), nil, "run"))

	assert.NotNil(t, got.Settings, "the factory should be built over this run's settings")
	assert.NotNil(t, got.Meter)
}

// TestRunPassesANilOrgResolver pins the placeholder: resolvable, so a constructor asking for one
// builds, and nil, since nothing builds one yet.
func TestRunPassesANilOrgResolver(t *testing.T) {
	var got orgresolver.OrgResolver
	resolved := false

	require.NoError(t, execute(t, logger.Test(t), provides(func(c do.Injector) (f *fake, err error) {
		got, err = do.Invoke[orgresolver.OrgResolver](c)
		resolved = err == nil
		return newFake(), err
	}), nil, "run"))

	assert.True(t, resolved)
	assert.Nil(t, got)
}

// TestRunPassesTheClock covers the run's clock, provided as the interface so a test can shadow it.
func TestRunPassesTheClock(t *testing.T) {
	var got clockwork.Clock

	require.NoError(t, execute(t, logger.Test(t), provides(func(c do.Injector) (f *fake, err error) {
		got, err = do.Invoke[clockwork.Clock](c)
		return newFake(), err
	}), nil, "run"))

	assert.NotNil(t, got)
}

// dependency is something a package registers with Provide from its init, built over what the run
// provides.
type dependency struct{ lggr logger.Logger }

// TestRunResolvesWhatInitRegistered covers the point of registering from init: a dependency
// registered before the run, depending on what only the run provides, is built against it.
func TestRunResolvesWhatInitRegistered(t *testing.T) {
	var got *dependency

	a := provides(func(c do.Injector) (f *fake, err error) {
		got, err = do.Invoke[*dependency](c)
		return newFake(), err
	}).withSetup(func() {
		Provide(func(i do.Injector) (*dependency, error) {
			lggr, err := do.Invoke[logger.Logger](i)
			return &dependency{lggr: lggr}, err
		})
	})

	require.NoError(t, execute(t, logger.Test(t), a, nil, "run"))
	require.NotNil(t, got)
	assert.NotNil(t, got.lggr, "the dependency should be built over the run's logger")
}

// TestProvideForRunAndForEmbed covers what an embedded instance does differently: each command
// resolves what was registered for it.
func TestProvideForRunAndForEmbed(t *testing.T) {
	setup := func() {
		ProvideForRun(func(do.Injector) (*dependency, error) { return &dependency{}, nil })
		ProvideForEmbed(func(do.Injector) (*dependency, error) { return &dependency{lggr: logger.Nop()}, nil })
	}
	var got *dependency
	a := provides(func(c do.Injector) (f *fake, err error) {
		got, err = do.Invoke[*dependency](c)
		return newFake(), err
	}).withSetup(setup)

	require.NoError(t, execute(t, logger.Test(t), a, nil, "run"))
	assert.Nil(t, got.lggr, "run resolves what was provided for run")

	require.NoError(t, execute(t, logger.Test(t), a, nil, "embed"))
	assert.NotNil(t, got.lggr, "embed resolves what was provided for embed")
}

// testConfig is the capability's own config, handed to Run through its ConfigProvider: what an
// operator supplies rather than the run builds.
type testConfig struct {
	// Shout is what the fake says.
	Shout string
}

// readsConfig is a capability with testConfig as its own config, defaulting to def, that records
// what it was handed.
func readsConfig(got *testConfig, def testConfig) app {
	return withConfig(provides(func(c do.Injector) (f *fake, err error) {
		*got, err = do.Invoke[testConfig](c)
		return newFake(), err
	}), NewConfigProvider(def))
}

// TestRunHandsTheConstructorItsConfig covers a registered config, bound under its namespace and
// resolved decoded.
func TestRunHandsTheConstructorItsConfig(t *testing.T) {
	var got testConfig

	require.NoError(t, execute(t, logger.Test(t), readsConfig(&got, testConfig{}), nil, "run", "--cron.shout", "hello"))

	assert.Equal(t, "hello", got.Shout)
}

// TestRunDefaultsTheCapabilityConfig covers NewConfigProvider's default: what the capability is
// handed when nothing sets it.
func TestRunDefaultsTheCapabilityConfig(t *testing.T) {
	var got testConfig

	require.NoError(t, execute(t, logger.Test(t), readsConfig(&got, testConfig{Shout: "default"}), nil, "run"))

	assert.Equal(t, "default", got.Shout)
}

// TestRunReadsTheCapabilityConfigFromTheEnvironment is the same binding from the other direction:
// CRE_, then the namespace, as every other section's settings are.
func TestRunReadsTheCapabilityConfigFromTheEnvironment(t *testing.T) {
	t.Setenv("CRE_CRON_SHOUT", "from the environment")

	var got testConfig

	require.NoError(t, execute(t, logger.Test(t), readsConfig(&got, testConfig{}), nil, "run"))

	assert.Equal(t, "from the environment", got.Shout)
}

// TestEmbedBindsRunsConfig covers a config registered with ProvideConfig reaching embed too.
func TestEmbedBindsRunsConfig(t *testing.T) {
	var got testConfig

	require.NoError(t, execute(t, logger.Test(t), readsConfig(&got, testConfig{}), nil, "embed", "--cron.shout", "embedded"))

	assert.Equal(t, "embedded", got.Shout)
}

// TestEmbedOnlyConfigIsNotOnRun covers ProvideConfigForEmbed: bound on embed alone.
func TestEmbedOnlyConfigIsNotOnRun(t *testing.T) {
	a := providesFake(newFake).withSetup(func() {
		ProvideConfigForEmbed("embedonly", testConfig{})
	})

	err := execute(t, logger.Test(t), a, nil, "run", "--embedonly.shout", "x")
	require.ErrorContains(t, err, "unknown flag: --embedonly.shout")
}

// TestRunRejectsADependencyRegisteringARunSetting pins the order: RunErr registers the run's own
// settings after every init, so a dependency that registered one of those types first is a
// duplicate, not an override.
func TestRunRejectsADependencyRegisteringARunSetting(t *testing.T) {
	a := providesFake(newFake).withSetup(func() {
		ProvideConfig("mine", HTTPConfig{Port: 1})
	})

	err := execute(t, logger.Test(t), a, nil, "run", "--http.port", "1")
	require.ErrorContains(t, err, "failed to register the run's settings")
}

// TestRunHasARunCommand is the shape of the binary rather than what it does: a root that runs
// nothing itself, and a way to start it hanging off it.
func TestRunHasARunCommand(t *testing.T) {
	// A bare invocation prints help and does nothing, rather than starting the capability.
	require.NoError(t, execute(t, logger.Test(t), app{ctor: func(do.Injector) (Capability, error) {
		t.Error("the root command should not build the capability")
		return newFake(), nil
	}}, nil))
}

// newFakeFor is a fake whose ID and service name name its instance: the run's one registry refuses
// an ID twice, and its health checker a name, so embedded instances have to differ in both.
func newFakeFor(e Instance) *fake {
	f := newFakeNamed(fmt.Sprintf("FakeCapability%d", e.ID))
	f.info.ID = fmt.Sprintf("fake-%d@1.0.0", e.ID)
	return f
}

// TestEmbedHostsEveryInstance covers --instances: one capability per instance, each built in a
// container of its own that tells it which instance it is and how many there are.
func TestEmbedHostsEveryInstance(t *testing.T) {
	var (
		mu  sync.Mutex
		got []Instance
	)

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (*fake, error) {
		e, err := do.Invoke[Instance](r)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
		return newFakeFor(e), nil
	}), nil, "embed", "--instances", "3"))

	assert.ElementsMatch(t, []Instance{
		{ID: 0, Total: 3},
		{ID: 1, Total: 3},
		{ID: 2, Total: 3},
	}, got)
}

// TestEmbedDefaultsToOneInstance pins the default, so embed with no flags is a run of one that
// still says it is embedded.
func TestEmbedDefaultsToOneInstance(t *testing.T) {
	var got Instance

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (f *fake, err error) {
		got, err = do.Invoke[Instance](r)
		return newFake(), err
	}), nil, "embed"))

	assert.Equal(t, Instance{ID: 0, Total: 1}, got)
}

// TestEmbedInstancesHaveTheirOwnResources covers what an instance is: each runs its own copy of
// the per-instance resources - here the registry - rather than sharing one.
func TestEmbedInstancesHaveTheirOwnResources(t *testing.T) {
	var (
		mu  sync.Mutex
		got []registry.CapabilitiesRegistry
	)

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (*fake, error) {
		e, err := do.Invoke[Instance](r)
		if err != nil {
			return nil, err
		}
		reg, err := do.Invoke[registry.CapabilitiesRegistry](r)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		got = append(got, reg)
		mu.Unlock()
		return newFakeFor(e), nil
	}), nil, "embed", "--instances", "2"))

	require.Len(t, got, 2)
	assert.NotSame(t, got[0], got[1])
}

// TestEmbedHasNoProxy covers embed's capabilities settings: an embedded run has no node behind
// it, so it has no --capabilities.proxy-url to dial one with.
func TestEmbedHasNoProxy(t *testing.T) {
	err := execute(t, logger.Test(t), providesFake(newFake), nil, "embed", "--capabilities.proxy-url", "x:1")
	require.ErrorContains(t, err, "unknown flag: --capabilities.proxy-url")
}

// TestEmbedDoesNotAnnounce is the same from the run's side: with no proxy, nothing reaches a
// node's registry even though one is listening.
func TestEmbedDoesNotAnnounce(t *testing.T) {
	node := newSpyRegistry(t)
	_ = serveNodeRegistry(t, node)

	require.NoError(t, execute(t, logger.Test(t), providesFake(newFake), nil, "embed"))

	assert.Empty(t, node.addedIDs())
}

// TestEmbedDefaultsTheDonID pins embed's default DON: one DON in one process, so 1 rather than
// the unset 0 a run starts from.
func TestEmbedDefaultsTheDonID(t *testing.T) {
	root, _, _ := settingsRoot(t)
	embedCmd, _, err := root.Find([]string{"embed"})
	require.NoError(t, err)

	don := embedCmd.PersistentFlags().Lookup("capabilities.capability-don-id")
	require.NotNil(t, don)
	assert.Equal(t, "1", don.DefValue)
}

// TestEmbedNamesEachInstancesLogger covers instance.N: several instances log apart, through the
// logger each resolves from its own container.
func TestEmbedNamesEachInstancesLogger(t *testing.T) {
	var (
		mu    sync.Mutex
		names = map[string]bool{}
	)

	require.NoError(t, execute(t, logger.Test(t), provides(func(c do.Injector) (*fake, error) {
		e, err := do.Invoke[Instance](c)
		if err != nil {
			return nil, err
		}
		lggr, err := do.Invoke[logger.Logger](c)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		names[lggr.Name()] = true
		mu.Unlock()
		return newFakeFor(e), nil
	}), nil, "embed", "--instances", "2"))

	assert.Equal(t, map[string]bool{"cron.instance.0": true, "cron.instance.1": true}, names)
}

// TestEmbedServesEachInstanceOnItsOwnPort covers http.port + ID: each instance has its own
// web server, and so its own /healthz.
func TestEmbedServesEachInstanceOnItsOwnPort(t *testing.T) {
	resetRegistrations(t)
	base := freePort(t)
	// The next port has to be free too; take two and use the lower.
	next := freePort(t)
	if next != base+1 {
		t.Skipf("ports %d and %d are not consecutive", base, next)
	}

	previous := os.Args
	t.Cleanup(func() { os.Args = previous })
	os.Args = []string{"cron", "embed", "--instances", "2", "--http.port", strconv.Itoa(base)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- RunErr(ctx, logger.Test(t), provides(func(r do.Injector) (*fake, error) {
			e, err := do.Invoke[Instance](r)
			return newFakeFor(e), err
		}).ctor, NewConfigProvider(struct{}{}))
	}()

	require.Eventually(t, func() bool { return serving(base) && serving(base+1) }, 30*time.Second,
		5*time.Millisecond, "both instances should be serving")
	cancel()
	require.NoError(t, <-done)
}

// TestEmbedRejectsNoInstances covers the floor: an embed hosting nothing is a mistake, not a run.
func TestEmbedRejectsNoInstances(t *testing.T) {
	err := execute(t, logger.Test(t), providesFake(newFake), nil, "embed", "--instances", "0", "--http.port", "1")
	require.ErrorContains(t, err, "Instances")
}

// TestRunIsOneInstance covers the run command's Instance: instance 0 of 1, so a capability may
// always ask which instance it is.
func TestRunIsOneInstance(t *testing.T) {
	var got Instance

	require.NoError(t, execute(t, logger.Test(t), provides(func(r do.Injector) (f *fake, err error) {
		got, err = do.Invoke[Instance](r)
		return newFake(), err
	}), nil, "run"))

	assert.Equal(t, Instance{ID: 0, Total: 1}, got)
}

// TestRunHasNoInstancesFlag pins --instances to embed: it is registered on that command alone.
func TestRunHasNoInstancesFlag(t *testing.T) {
	err := execute(t, logger.Test(t), providesFake(newFake), nil, "run", "--instances", "2")
	require.ErrorContains(t, err, "unknown flag: --instances")
}

// TestRunServesAndAnnouncesTheCapability is the whole path through the real entry point: a run
// serves its capability, tells the node's registry where, and takes it back on shutdown.
//
// The removal half is reliable to assert here because the registry's close is one ordered
// function: the Remove RPC is sent before the connection it travels on is closed.
func TestRunServesAndAnnouncesTheCapability(t *testing.T) {
	node := newSpyRegistry(t)
	proxyURL := serveNodeRegistry(t, node)

	require.NoError(t, execute(t, logger.Test(t), providesFake(newFake), nil,
		"run", "--capabilities.proxy-url", proxyURL))

	assert.Contains(t, node.addedIDs(), fakeID, "the run should have announced the capability to the node's registry")
	assert.Contains(t, node.removedIDs(), fakeID, "and deregistered it on the way out")
}
