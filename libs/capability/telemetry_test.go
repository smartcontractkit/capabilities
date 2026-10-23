package capability

import (
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

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

	// The production defaults, so what these read is what a binary gets rather than a zero value.
	cfg := defaultConfig()
	_, err := cfg.bind(root)
	require.NoError(t, err)
	return root, cfg
}

// TestEveryFlagIsDocumented fails when a config field has no doc comment, or doccomments_gen.go is
// stale: run go generate.
//
// The exceptions are telemetry.TelemetryConfig's flags: docs are read from a DocComments method on the
// owning struct, and generating one in pkg/loop would make chainlink-common import x/config, which
// already depends on it. Documenting any of these fails this too, so the list stays accurate.
func TestEveryFlagIsDocumented(t *testing.T) {
	b, err := defaultConfig().bind(&cobra.Command{Use: "cron"})
	require.NoError(t, err)
	require.Equal(t, undocumentedLoopFlags, b.Undocumented())
}

var undocumentedLoopFlags = []string{
	"chip-ingress-batch-emitter-enabled",
	"chip-ingress-buffer-size",
	"chip-ingress-drain-timeout",
	"chip-ingress-durable-emitter-enabled",
	"chip-ingress-endpoint",
	"chip-ingress-insecure-connection",
	"chip-ingress-max-batch-size",
	"chip-ingress-max-concurrent-sends",
	"chip-ingress-max-grpc-request-size",
	"chip-ingress-send-interval",
	"chip-ingress-send-timeout",
	"grpc.advertise-host",
	"grpc.server-max-recv-msg-size",
	"pyroscope-auth-token",
	"pyroscope-environment",
	"pyroscope-link-traces-to-profiles",
	"pyroscope-pprof-block-profile-rate",
	"pyroscope-pprof-mutex-profile-fraction",
	"pyroscope-server-address",
	"telemetry-attribute",
	"telemetry-attribute.<key>",
	"telemetry-auth-header",
	"telemetry-auth-header.<key>",
	"telemetry-auth-headers-ttl",
	"telemetry-auth-pub-key-hex",
	"telemetry-ca-cert-file",
	"telemetry-emitter-batch-processor",
	"telemetry-emitter-export-interval",
	"telemetry-emitter-export-max-batch-size",
	"telemetry-emitter-export-timeout",
	"telemetry-emitter-max-queue-size",
	"telemetry-enabled",
	"telemetry-endpoint",
	"telemetry-insecure-connection",
	"telemetry-log-batch-processor",
	"telemetry-log-compressor",
	"telemetry-log-export-interval",
	"telemetry-log-export-max-batch-size",
	"telemetry-log-export-timeout",
	"telemetry-log-level",
	"telemetry-log-max-queue-size",
	"telemetry-log-streaming-enabled",
	"telemetry-metric-cardinality-limit",
	"telemetry-metric-compressor",
	"telemetry-metric-export-batch-size",
	"telemetry-metric-views-deny-attributes",
	"telemetry-prometheus-bridge-enabled",
	"telemetry-prometheus-bridge-prefixes",
	"telemetry-trace-compressor",
	"telemetry-trace-sample-ratio",
	"tracing-attribute",
	"tracing-attribute.<key>",
	"tracing-collector-target",
	"tracing-enabled",
	"tracing-sampling-ratio",
	"tracing-tls-cert-path",
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
		"telemetry-endpoint",
		"telemetry-insecure-connection",
		"telemetry-ca-cert-file",
		"telemetry-attribute",
		"telemetry-auth-header",
		"telemetry-auth-pub-key-hex",
		"telemetry-auth-headers-ttl",
		"telemetry-prometheus-bridge-enabled",

		"tracing-enabled",
		"tracing-collector-target",
		"tracing-sampling-ratio",
		"tracing-tls-cert-path",

		"chip-ingress-endpoint",
		"chip-ingress-insecure-connection",

		"pyroscope-server-address",
		"pyroscope-auth-token",
		"pyroscope-environment",

		"http.port",

		"app-id",

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

	sampling := root.PersistentFlags().Lookup("tracing-sampling-ratio")
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
		"--tracing-enabled", "true",
		"--tracing-sampling-ratio", "0.25",
		"--tracing-tls-cert-path", "/certs/trace.pem",
		"--chip-ingress-endpoint", "chip:9000",
		"--chip-ingress-insecure-connection", "true",
		"--pyroscope-server-address", "pyro:4040",
		"--pyroscope-environment", "staging",
		"--telemetry-endpoint", "otel:4317",
		"--telemetry-insecure-connection", "true",
		"--telemetry-attribute", "env=staging",
		"--telemetry-auth-headers-ttl", "5m",
		"--telemetry-prometheus-bridge-enabled", "true",
	)

	obs := cfg.observability
	assert.Equal(t, "otel:4317", obs.TelemetryEndpoint)
	assert.True(t, obs.TelemetryInsecureConnection)
	assert.Equal(t, beholder.OtelAttributes{"env": "staging"}, obs.TelemetryAttributes)
	assert.Equal(t, 5*time.Minute, obs.TelemetryAuthHeadersTTL)
	assert.True(t, obs.TelemetryPrometheusBridgeEnabled)

	assert.True(t, obs.TracingEnabled)
	assert.InDelta(t, 0.25, obs.TracingSamplingRatio, 0)
	assert.Equal(t, "/certs/trace.pem", obs.TracingTLSCertPath)

	assert.Equal(t, "chip:9000", obs.ChipIngressEndpoint)
	assert.True(t, obs.ChipIngressInsecureConnection)

	assert.Equal(t, "pyro:4040", obs.PyroscopeServerAddress)
	assert.Equal(t, "staging", obs.PyroscopeEnvironment)
}

// TestTelemetryDecodesFromEnv covers why the namespace and field names are what they are: a
// go-plugin host sets CL_TELEMETRY_ENDPOINT, and a binary it starts has to pick that up without
// being passed a flag.
func TestObservabilityDecodesFromEnv(t *testing.T) {
	t.Setenv("CL_TELEMETRY_ENDPOINT", "from-env:4317")

	assert.Equal(t, "from-env:4317", decoded(t).observability.TelemetryEndpoint)
}

// TestTelemetryDefaultsAreBeholders covers the settings a binary run without a LOOP host gets: they
// are beholder's defaults rather than zero values, except log streaming, which stderr already covers.
func TestTelemetryDefaultsAreBeholders(t *testing.T) {
	cfg := defaultObservability().TelemetryConfig

	defaults := beholder.DefaultConfig()
	assert.Equal(t, defaults.EmitterExportMaxBatchSize, cfg.TelemetryEmitterExportMaxBatchSize)
	assert.Empty(t, cfg.TelemetryMetricCompressor, "an empty env var could not override it")
	assert.Equal(t, defaults.ChipIngressBufferSize, cfg.ChipIngressBufferSize)
	require.NotNil(t, cfg.TelemetryMetricCardinalityLimit)
	assert.Equal(t, defaults.MetricCardinalityLimit, *cfg.TelemetryMetricCardinalityLimit)
	assert.False(t, cfg.TelemetryLogStreamingEnabled)
}

// TestWithOtelViewsReachesTheClient is the whole of what the option is for: the views a binary
// passes are the ones the beholder client is built with.
//
// They cannot arrive any later - the OTEL specification requires histogram buckets at client
// creation - which is why this is an option on Run rather than something the capability declares.
func TestWithOtelViewsReachesTheClient(t *testing.T) {
	view := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "cron_capability_*"},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: []float64{1, 2, 3}}},
	)

	cfg := defaultConfig()
	WithOtelViews(view)(cfg)

	assert.Len(t, cfg.observability.otelViews, 1, "the view should be what the client is built with")
}

// TestWithOtelViewsDefaultsToNone covers a binary that passes none: the client keeps whatever
// beholder's own defaults are rather than being handed an empty list to mean something.
func TestWithOtelViewsDefaultsToNone(t *testing.T) {
	assert.Empty(t, defaultObservability().otelViews)
}

// TestRunUsesTheDecodedTelemetrySettings is the whole path in one: a flag on the command line
// reaches the config the beholder client is built from.
//
// A malformed attribute is what makes that visible without a reachable collector - decoding fails
// before a client is ever created - and an unregistered flag would have failed with cobra's
// "unknown flag" instead.
func TestRunUsesTheDecodedTelemetrySettings(t *testing.T) {
	err := execute(t, logger.Test(t), fakeConstructor, "run",
		"--telemetry-endpoint", "otel:4317", "--telemetry-attribute", "nope")

	require.ErrorContains(t, err, "telemetry-attribute")
	require.NotContains(t, err.Error(), "unknown flag")
}

// TestStartTelemetryWithoutAnEndpointChangesNothing is the off case: no endpoint means no client,
// so nothing is installed, nothing joins the root, and there is nothing to undo.
func TestStartTelemetryWithoutAnEndpointChangesNothing(t *testing.T) {
	before := beholder.GetClient()

	telemetry, err := startTelemetry(t.Context(), logger.Test(t), &observability{})
	require.NoError(t, err)

	// A service that does nothing rather than a nil, so the caller has one thing to hand the root
	// either way. No client behind it: the process keeps the noop beholder client it already had.
	require.NotNil(t, telemetry)
	assert.Nil(t, telemetry.Client(), "there is no client when telemetry is off")
	assert.Same(t, before, beholder.GetClient(), "nothing should have been installed")

	require.NoError(t, telemetry.Close())
	assert.Same(t, before, beholder.GetClient(), "and closing it changes nothing")
}

// TestRunLeavesTelemetryAsItFoundIt is the same guarantee seen from the binary: a run that started
// telemetry and finished has put the process back the way it was.
func TestRunLeavesTelemetryAsItFoundIt(t *testing.T) {
	before := beholder.GetClient()

	require.NoError(t, execute(t, logger.Test(t), fakeConstructor, "run"))

	assert.Same(t, before, beholder.GetClient())
}
