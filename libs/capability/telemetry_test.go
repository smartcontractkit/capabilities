package capability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// settingsRoot builds the command tree the way RunErr does, through the same registration, and
// returns what the flags are bound to.
//
// The registration is the production one, so what these tests read is what a binary gets.
func settingsRoot(t *testing.T) (*cobra.Command, *config) {
	t.Helper()

	root := &cobra.Command{Use: "cron"}
	root.PersistentFlags().String("config", "", "Path to config file")

	// The production defaults, so what these read is what a binary gets rather than a zero value.
	cfg := defaultConfig()
	require.NoError(t, cfg.register(root))
	return root, cfg
}

// decoded runs args through the command tree and returns the settings as they were decoded.
func decoded(t *testing.T, args ...string) config {
	t.Helper()

	root, cfg := settingsRoot(t)
	root.AddCommand(&cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }})

	args = append([]string{"run"}, args...)
	root.SetArgs(args)
	require.NoError(t, root.Execute())
	return *cfg
}

// TestTelemetryFlags pins the surface the beholder client's settings have: a flag per field, named
// after the service that owns it. A binary gets these without asking, because every binary has them.
func TestObservabilityFlags(t *testing.T) {
	root, _ := settingsRoot(t)

	for _, name := range []string{
		"telemetry.endpoint",
		"telemetry.insecure-connection",
		"telemetry.ca-cert-file",
		"telemetry.attributes",
		"telemetry.auth-headers",
		"telemetry.auth-pub-key-hex",
		"telemetry.auth-headers-ttl",
		"telemetry.prometheus-bridge-enabled",

		"tracing.enabled",
		"tracing.sampling-ratio",
		"tracing.tls-cert-file",

		"chip-ingress.endpoint",
		"chip-ingress.insecure-connection",

		"pyroscope.server-address",
		"pyroscope.auth-token",
		"pyroscope.environment",

		"http.port",

		"capabilities.proxy-url",
		"capabilities.capability-don-id",
	} {
		assert.NotNil(t, root.PersistentFlags().Lookup(name), "missing flag --%s", name)
	}
}

// TestCapabilitiesDecodeFromFlags covers the sibling section: what a capability binary needs from
// the node it runs beside, which is not observability and so is configured separately.
func TestCapabilitiesDecodeFromFlags(t *testing.T) {
	cfg := decoded(t, "--capabilities.proxy-url", "dns:///registry.internal:9000",
		"--capabilities.capability-don-id", "7")

	assert.Equal(t, "dns:///registry.internal:9000", cfg.capabilities.ProxyURL)
	assert.Equal(t, uint32(7), cfg.capabilities.CapabilityDonID)
}

// TestCapabilitiesAreOptional covers both settings being optional: a binary with no node behind it
// starts, and resolves only the capabilities it hosts.
//
// --http.port is the one thing a run still cannot do without, which is what the args below are.
func TestCapabilitiesAreOptional(t *testing.T) {
	root, cfg := settingsRoot(t)
	root.AddCommand(&cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }})
	root.SetArgs([]string{"run", "--http.port", "1"})

	require.NoError(t, root.Execute())
	assert.Empty(t, cfg.capabilities.ProxyURL)
	assert.Zero(t, cfg.capabilities.CapabilityDonID)
}

// TestObservabilityDefaults pins the settings that are not their zero value, since those are the
// ones an operator gets without asking.
func TestObservabilityDefaults(t *testing.T) {
	root, _ := settingsRoot(t)

	sampling := root.PersistentFlags().Lookup("tracing.sampling-ratio")
	require.NotNil(t, sampling)
	assert.Equal(t, "1", sampling.DefValue, "sampling every trace is the default")

	port := root.PersistentFlags().Lookup("http.port")
	require.NotNil(t, port)
	assert.Equal(t, "8080", port.DefValue)
}

// TestBinaryStartsWithNoSettingsAtAll is what defaulting the port bought: nothing has to be
// configured for a run to be a valid one.
//
// It is the whole surface in one assertion - every setting this binary has is now either defaulted
// or genuinely optional - so a flag gaining `validate:"required"` fails here.
func TestBinaryStartsWithNoSettingsAtAll(t *testing.T) {
	root, cfg := settingsRoot(t)
	root.AddCommand(&cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }})
	root.SetArgs([]string{"run"})
	root.SilenceUsage, root.SilenceErrors = true, true

	require.NoError(t, root.Execute())
	assert.Equal(t, uint16(defaultHTTPPort), cfg.observability.http.Port)
}

// TestTelemetryDecodesFromFlags is the point of registering them: what an operator types reaches the
// struct the beholder client is built from.
func TestObservabilityDecodesFromFlags(t *testing.T) {
	cfg := decoded(t,
		"--tracing.enabled", "true",
		"--tracing.sampling-ratio", "0.25",
		"--tracing.tls-cert-file", "/certs/trace.pem",
		"--chip-ingress.endpoint", "chip:9000",
		"--chip-ingress.insecure-connection", "true",
		"--pyroscope.server-address", "pyro:4040",
		"--pyroscope.environment", "staging",
		"--telemetry.endpoint", "otel:4317",
		"--telemetry.insecure-connection", "true",
		"--telemetry.attributes", "env=staging",
		"--telemetry.auth-headers-ttl", "5m",
		"--telemetry.prometheus-bridge-enabled", "true",
	)

	assert.Equal(t, "otel:4317", cfg.observability.telemetry.Endpoint)
	assert.True(t, cfg.observability.telemetry.InsecureConnection)
	assert.Equal(t, []string{"env=staging"}, cfg.observability.telemetry.Attributes)
	assert.Equal(t, 5*time.Minute, cfg.observability.telemetry.AuthHeadersTTL.Duration())
	assert.True(t, cfg.observability.telemetry.PrometheusBridgeEnabled)

	assert.True(t, cfg.observability.tracing.Enabled)
	assert.InDelta(t, 0.25, cfg.observability.tracing.SamplingRatio, 0)
	assert.Equal(t, "/certs/trace.pem", cfg.observability.tracing.TLSCertFile)

	assert.Equal(t, "chip:9000", cfg.observability.chipIngress.Endpoint)
	assert.True(t, cfg.observability.chipIngress.InsecureConnection)

	assert.Equal(t, "pyro:4040", cfg.observability.pyroscope.ServerAddress)
	assert.Equal(t, "staging", cfg.observability.pyroscope.Environment)
}

// TestChipIngressFoldsIntoTheBeholderClient covers the shape of these three: chip ingress and
// tracing are configured separately but exported through the one client, so what turns them on is
// a field on its config rather than a service of their own.
func TestChipIngressFoldsIntoTheBeholderClient(t *testing.T) {
	off, err := beholderConfig(logger.Test(t), &observability{telemetry: TelemetryConfig{Endpoint: "otel:4317"}})
	require.NoError(t, err)
	assert.False(t, off.ChipIngressEmitterEnabled, "an unset endpoint leaves the emitter off")

	on, err := beholderConfig(logger.Test(t), &observability{
		telemetry:   TelemetryConfig{Endpoint: "otel:4317"},
		chipIngress: ChipIngressConfig{Endpoint: "chip:9000", InsecureConnection: true},
	})
	require.NoError(t, err)
	assert.True(t, on.ChipIngressEmitterEnabled, "setting an endpoint is what enables it")
	assert.Equal(t, "chip:9000", on.ChipIngressEmitterGRPCEndpoint)
	assert.True(t, on.ChipIngressInsecureConnection)
}

// TestTracingFoldsIntoTheBeholderClient is the same for traces: they go to the telemetry endpoint,
// so enabling them adds an exporter to the client rather than standing anything else up.
func TestTracingFoldsIntoTheBeholderClient(t *testing.T) {
	lggr, _ := detachedLogger()

	off, err := beholderConfig(lggr, &observability{telemetry: TelemetryConfig{Endpoint: "otel:4317"}})
	require.NoError(t, err)
	assert.Nil(t, off.TraceSpanExporter)

	on, err := beholderConfig(lggr, &observability{
		telemetry: TelemetryConfig{Endpoint: "otel:4317"},
		tracing:   TracingConfig{Enabled: true, SamplingRatio: 0.25},
	})
	require.NoError(t, err)
	require.NotNil(t, on.TraceSpanExporter, "enabling tracing should add an exporter")
	assert.InDelta(t, 0.25, on.TraceSampleRatio, 0)

	// The exporter dials its collector on a goroutine of its own, so it has to be shut down or it
	// outlives the test that made it.
	require.NoError(t, on.TraceSpanExporter.Shutdown(context.Background()))
}

// TestTracingSurvivesAnUnreachableCollector covers the dialer's error path, which is not optional:
// loop.TracingConfig calls OnDialError without checking it is set, so an exporter built without one
// turns an unreachable collector into a nil dereference that takes the process with it.
func TestTracingSurvivesAnUnreachableCollector(t *testing.T) {
	lggr, logs := detachedLogger()

	// A port nothing is listening on, so the dial is guaranteed to fail.
	cfg, err := beholderConfig(lggr, &observability{
		telemetry: TelemetryConfig{Endpoint: fmt.Sprintf("localhost:%d", freePort(t))},
		tracing:   TracingConfig{Enabled: true, SamplingRatio: 1},
	})
	require.NoError(t, err)
	require.NotNil(t, cfg.TraceSpanExporter)

	// Exporting is what makes it dial, and the dial is asynchronous - so the failure is waited for
	// rather than asserted straight away. Reaching this log at all is the point: without
	// OnDialError the same path is a nil dereference, which would take the test binary with it
	// rather than failing this assertion.
	_ = cfg.TraceSpanExporter.ExportSpans(context.Background(), nil)

	require.Eventually(t, func() bool {
		return len(logs.FilterMessage("Failed to dial the tracing collector").All()) > 0
	}, 10*time.Second, 10*time.Millisecond, "the dial failure should be reported rather than swallowed")

	require.NoError(t, cfg.TraceSpanExporter.Shutdown(context.Background()))
}

// TestTelemetryDecodesFromEnv covers why the namespace and field names are what they are: a
// go-plugin host sets CL_TELEMETRY_ENDPOINT, and a binary it starts has to pick that up without
// being passed a flag.
func TestObservabilityDecodesFromEnv(t *testing.T) {
	t.Setenv("CL_TELEMETRY_ENDPOINT", "from-env:4317")

	assert.Equal(t, "from-env:4317", decoded(t).observability.telemetry.Endpoint)
}

// TestBeholderConfigMergesLegacyEnvPairs covers the other half of that contract: a host encodes a
// map as one env var per entry, and the setting wins where both name the same key.
func TestBeholderConfigMergesLegacyEnvPairs(t *testing.T) {
	t.Setenv(envTelemetryAttributePrefix+"region", "us-east")
	t.Setenv(envTelemetryAttributePrefix+"env", "from-env")

	cfg, err := beholderConfig(logger.Test(t), &observability{
		telemetry: TelemetryConfig{Endpoint: "otel:4317", Attributes: []string{"env=from-setting"}},
	})
	require.NoError(t, err)

	attributes := map[string]string{}
	for _, a := range cfg.ResourceAttributes {
		attributes[string(a.Key)] = a.Value.AsString()
	}
	assert.Equal(t, "us-east", attributes["region"], "the env-only entry should survive")
	assert.Equal(t, "from-setting", attributes["env"], "the setting should win over the env var")
}

// TestWithOtelViewsReachesTheClient is the whole of what the option is for: the views a binary
// passes are on the config the beholder client is built from.
//
// They cannot arrive any later - the OTEL specification requires histogram buckets at client
// creation - which is why this is an option on Run rather than something the capability declares.
func TestWithOtelViewsReachesTheClient(t *testing.T) {
	view := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "cron_capability_*"},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: []float64{1, 2, 3}}},
	)

	obs := defaultObservability()
	obs.telemetry.Endpoint = "otel:4317"
	WithOtelViews(view)(obs)

	cfg, err := beholderConfig(logger.Test(t), obs)
	require.NoError(t, err)
	assert.Len(t, cfg.MetricViews, 1, "the view should be on the config the client is built from")
}

// TestWithOtelViewsDefaultsToNone covers a binary that passes none: the client keeps whatever
// beholder's own defaults are rather than being handed an empty list to mean something.
func TestWithOtelViewsDefaultsToNone(t *testing.T) {
	obs := defaultObservability()
	obs.telemetry.Endpoint = "otel:4317"

	cfg, err := beholderConfig(logger.Test(t), obs)
	require.NoError(t, err)
	assert.Empty(t, cfg.MetricViews)
}

// TestRunUsesTheDecodedTelemetrySettings is the whole path in one: a flag on the command line
// reaches the config the beholder client is built from.
//
// A malformed attribute is what makes that visible without a reachable collector - building the
// config fails before a client is ever created - and an unregistered flag would have failed
// earlier, with cobra's "unknown flag" instead.
func TestRunUsesTheDecodedTelemetrySettings(t *testing.T) {
	err := execute(t, logger.Test(t), newFake, "run",
		"--telemetry.endpoint", "otel:4317", "--telemetry.attributes", "nope")

	require.ErrorContains(t, err, `invalid telemetry.attributes entry "nope": expected key=value`)
}

// TestStartTelemetryWithoutAnEndpointChangesNothing is the off case: no endpoint means no client,
// so nothing is installed, nothing joins the root, and there is nothing to undo.
func TestStartTelemetryWithoutAnEndpointChangesNothing(t *testing.T) {
	before := beholder.GetClient()

	telemetry, err := newTelemetry(logger.Test(t), &observability{})
	require.NoError(t, err)

	// A service that does nothing rather than a nil, so the caller has one thing to hand the root
	// either way. No client behind it: the process keeps the noop beholder client it already had.
	require.NotNil(t, telemetry)
	assert.Nil(t, telemetry.client, "there is no client when telemetry is off")
	assert.Same(t, before, beholder.GetClient(), "nothing should have been installed")

	require.NoError(t, telemetry.Start(t.Context()))
	require.NoError(t, telemetry.Close())
	assert.Same(t, before, beholder.GetClient(), "and starting or closing it changes nothing")
}

// TestTelemetryServiceRestoresTheGlobalWhenClosed is what making telemetry a service bought: what
// starting it changed about the process is undone by closing it, rather than by the run remembering
// to.
// TestTelemetryServiceRestoresTheGlobalWhenClosed is what making telemetry a service bought: the
// run hands it to the root and forgets about it, and closing it is what puts the process back.
func TestTelemetryServiceRestoresTheGlobalWhenClosed(t *testing.T) {
	original := beholder.GetClient()
	client := beholder.NewNoopClient()
	require.NotSame(t, original, client)

	svc := newTelemetryService(logger.Test(t), client, installGlobally(client))
	require.Same(t, client, beholder.GetClient(), "installing happens when it is built, not when it starts")

	require.NoError(t, svc.Start(t.Context()))
	require.NoError(t, svc.Close())
	assert.Same(t, original, beholder.GetClient(), "closing should put the previous client back")
}

// TestInstallGloballyIsReversible is the reversibility itself: installing a client replaces the
// process's, and what comes back puts the previous one in its place.
//
// This is the only lasting change starting telemetry makes - everything else belongs to the client
// - so it is the thing worth pinning. It is tested here rather than through startBeholder because
// that would need a live OTLP endpoint to get as far as installing anything.
func TestInstallGloballyIsReversible(t *testing.T) {
	original := beholder.GetClient()
	client := beholder.NewNoopClient()
	require.NotSame(t, original, client)

	restore := installGlobally(client)
	assert.Same(t, client, beholder.GetClient(), "the new client should be the process's")

	restore()
	assert.Same(t, original, beholder.GetClient(), "the previous client should be back")
}

// TestInstallGloballyNests covers a second install on top of a first: each restore puts back what
// that install replaced, so unwinding in reverse arrives where it started.
func TestInstallGloballyNests(t *testing.T) {
	original := beholder.GetClient()
	first, second := beholder.NewNoopClient(), beholder.NewNoopClient()

	restoreFirst := installGlobally(first)
	restoreSecond := installGlobally(second)
	assert.Same(t, second, beholder.GetClient())

	restoreSecond()
	assert.Same(t, first, beholder.GetClient())

	restoreFirst()
	assert.Same(t, original, beholder.GetClient())
}

// TestRunLeavesTelemetryAsItFoundIt is the same guarantee seen from the binary: a run that started
// telemetry and finished has put the process back the way it was.
func TestRunLeavesTelemetryAsItFoundIt(t *testing.T) {
	before := beholder.GetClient()

	require.NoError(t, execute(t, logger.Test(t), newFake, "run"))

	assert.Same(t, before, beholder.GetClient())
}

// detachedLogger is an observed logger that is not bound to t.
//
// A trace exporter dials its collector on a goroutine of its own and keeps retrying, so it outlives
// the test that made it - and logger.Test panics on anything logged after t has finished, which
// would make these tests fail for a reason that has nothing to do with what they check.
func detachedLogger() (logger.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.ErrorLevel)
	return logger.NewWithCores(core), logs
}
