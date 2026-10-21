package capability

import (
	"os"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap/zapcore"

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
func TestEveryFlagIsDocumented(t *testing.T) {
	b, err := defaultConfig().bind(&cobra.Command{Use: "cron"})
	require.NoError(t, err)
	require.Empty(t, b.Undocumented())
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
		"telemetry.prometheus-bridge-prefixes",
		"telemetry.app-id",
		"telemetry.trace-sample-ratio",
		"telemetry.emitter-batch-processor",
		"telemetry.emitter-export-timeout",
		"telemetry.log-streaming-enabled",
		"telemetry.log-level",
		"telemetry.metric-cardinality-limit",
		"telemetry.metric-views-deny-attributes",

		"tracing.enabled",
		"tracing.sampling-ratio",
		"tracing.tls-cert-file",
		"tracing.collector-target",
		"tracing.attributes",

		"chip-ingress.endpoint",
		"chip-ingress.insecure-connection",
		"chip-ingress.batch-emitter-enabled",
		"chip-ingress.max-grpc-request-size",

		"pyroscope.server-address",
		"pyroscope.auth-token",
		"pyroscope.environment",
		"pyroscope.link-traces-to-profiles",
		"pyroscope.pprof-block-profile-rate",
		"pyroscope.pprof-mutex-profile-fraction",

		"http.port",

		"capabilities.proxy-url",
		"capabilities.capability-don-id",

		"grpc.server-max-recv-msg-size",
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

	streaming := root.PersistentFlags().Lookup("telemetry.log-streaming-enabled")
	require.NotNil(t, streaming)
	assert.Empty(t, streaming.DefValue, "logs already reach stderr, so streaming them is off")

	timeout := root.PersistentFlags().Lookup("telemetry.emitter-export-timeout")
	require.NotNil(t, timeout)
	assert.Equal(t, beholder.DefaultConfig().EmitterExportTimeout.String(), timeout.DefValue,
		"the beholder client's own defaults, since loop.Server does not apply them")
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

// TestEnvConfigEnablesTelemetryWithAnEndpoint covers the gate loop.Server's env parsing has and this
// config does not: CL_TELEMETRY_ENDPOINT is ignored unless CL_TELEMETRY_ENABLED is set too.
func TestEnvConfigEnablesTelemetryWithAnEndpoint(t *testing.T) {
	off, err := envConfig(defaultObservability(), GRPCConfig{})
	require.NoError(t, err)
	assert.False(t, off.TelemetryEnabled)

	obs := defaultObservability()
	obs.telemetry.Endpoint = "otel:4317"
	on, err := envConfig(obs, GRPCConfig{})
	require.NoError(t, err)
	assert.True(t, on.TelemetryEnabled)
	assert.Equal(t, "otel:4317", on.TelemetryEndpoint)
}

// TestEnvConfigTracesToTheTelemetryEndpointByDefault keeps what this binary did before it ran on
// loop.Server, which has a collector of its own: traces go to the telemetry endpoint unless told
// otherwise.
func TestEnvConfigTracesToTheTelemetryEndpointByDefault(t *testing.T) {
	obs := defaultObservability()
	obs.telemetry.Endpoint = "otel:4317"

	env, err := envConfig(obs, GRPCConfig{})
	require.NoError(t, err)
	assert.Equal(t, "otel:4317", env.TracingCollectorTarget)

	obs.tracing.CollectorTarget = "traces:4317"
	env, err = envConfig(obs, GRPCConfig{})
	require.NoError(t, err)
	assert.Equal(t, "traces:4317", env.TracingCollectorTarget)
}

// TestEnvConfigSetsTheCardinalityLimit covers the one pointer loop.Server dereferences unchecked:
// left nil, a run with telemetry configured panics on the way up.
func TestEnvConfigSetsTheCardinalityLimit(t *testing.T) {
	env, err := envConfig(defaultObservability(), GRPCConfig{})
	require.NoError(t, err)
	require.NotNil(t, env.TelemetryMetricCardinalityLimit)
	assert.Equal(t, beholder.DefaultMetricCardinalityLimit, *env.TelemetryMetricCardinalityLimit)
}

// TestTelemetryDecodesFromEnv covers why the namespace and field names are what they are: a
// go-plugin host sets CL_TELEMETRY_ENDPOINT, and a binary it starts has to pick that up without
// being passed a flag.
func TestObservabilityDecodesFromEnv(t *testing.T) {
	t.Setenv("CL_TELEMETRY_ENDPOINT", "from-env:4317")

	assert.Equal(t, "from-env:4317", decoded(t).observability.telemetry.Endpoint)
}

// TestEnvConfigMergesLegacyEnvPairs covers the other half of that contract: a host encodes a map as
// one env var per entry, and the setting wins where both name the same key.
func TestEnvConfigMergesLegacyEnvPairs(t *testing.T) {
	t.Setenv(envTelemetryAttributePrefix+"region", "us-east")
	t.Setenv(envTelemetryAttributePrefix+"env", "from-env")
	t.Setenv(envTracingAttributePrefix+"zone", "a")

	obs := defaultObservability()
	obs.telemetry.Attributes = []string{"env=from-setting"}
	env, err := envConfig(obs, GRPCConfig{})
	require.NoError(t, err)

	assert.Equal(t, "us-east", env.TelemetryAttributes["region"], "the env-only entry should survive")
	assert.Equal(t, "from-setting", env.TelemetryAttributes["env"], "the setting should win over the env var")
	assert.Equal(t, "a", env.TracingAttributes["zone"])
}

// TestEnvNamesMatchTheHost is why the fields are named after loop.EnvConfig's: each flag's env var
// is the one a plugin host already sets, so the host needs no new names for these.
func TestEnvNamesMatchTheHost(t *testing.T) {
	for name, value := range map[string]string{
		"CL_TELEMETRY_EMITTER_EXPORT_MAX_BATCH_SIZE": "7",
		"CL_TELEMETRY_METRIC_CARDINALITY_LIMIT":      "8",
		"CL_TELEMETRY_LOG_LEVEL":                     "warn",
		"CL_TRACING_COLLECTOR_TARGET":                "traces:4317",
		"CL_CHIP_INGRESS_MAX_GRPC_REQUEST_SIZE":      "9",
		"CL_CHIP_INGRESS_SEND_TIMEOUT":               "3s",
		"CL_PYROSCOPE_PPROF_BLOCK_PROFILE_RATE":      "10",
		"CL_GRPC_SERVER_MAX_RECV_MSG_SIZE":           "11",
	} {
		t.Setenv(name, value)
	}

	cfg := decoded(t)
	assert.Equal(t, 7, cfg.observability.telemetry.EmitterExportMaxBatchSize)
	assert.Equal(t, 8, cfg.observability.telemetry.MetricCardinalityLimit)
	assert.Equal(t, zapcore.WarnLevel, cfg.observability.telemetry.LogLevel)
	assert.Equal(t, "traces:4317", cfg.observability.tracing.CollectorTarget)
	assert.Equal(t, 9, cfg.observability.chipIngress.MaxGRPCRequestSize)
	assert.Equal(t, 3*time.Second, cfg.observability.chipIngress.SendTimeout.Duration())
	assert.Equal(t, 10, cfg.observability.pyroscope.PPROFBlockProfileRate)
	assert.Equal(t, 11, cfg.grpc.ServerMaxRecvMsgSize)
}

// TestSetEnvRestores is what keeps the shim from leaking: the env loop.Server reads is the
// process's only while it reads it.
func TestSetEnvRestores(t *testing.T) {
	t.Setenv("CL_TEST_SET", "before")
	require.NoError(t, os.Unsetenv("CL_TEST_UNSET"))

	restore, err := setEnv([]string{"CL_TEST_SET=during", "CL_TEST_UNSET=during"})
	require.NoError(t, err)
	assert.Equal(t, "during", os.Getenv("CL_TEST_SET"))
	assert.Equal(t, "during", os.Getenv("CL_TEST_UNSET"))

	restore()
	assert.Equal(t, "before", os.Getenv("CL_TEST_SET"))
	_, set := os.LookupEnv("CL_TEST_UNSET")
	assert.False(t, set)
}

// TestWithOtelViewsReachesTheConfig is the whole of what the option is for: the views a binary
// passes are what the run hands loop.Server, which builds the beholder client with them.
func TestWithOtelViewsReachesTheConfig(t *testing.T) {
	view := sdkmetric.NewView(
		sdkmetric.Instrument{Name: "cron_capability_*"},
		sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{Boundaries: []float64{1, 2, 3}}},
	)

	cfg := defaultConfig()
	WithOtelViews(view)(cfg)
	assert.Len(t, cfg.observability.otelViews, 1)
}

// TestRunUsesTheDecodedTelemetrySettings is the whole path in one: a flag on the command line
// reaches the config the beholder client is built from.
//
// A malformed attribute is what makes that visible without a reachable collector - building the
// config fails before a client is ever created - and an unregistered flag would have failed
// earlier, with cobra's "unknown flag" instead.
func TestRunUsesTheDecodedTelemetrySettings(t *testing.T) {
	err := execute(t, logger.Test(t), fakeConstructor, "run",
		"--telemetry.endpoint", "otel:4317", "--telemetry.attributes", "nope")

	require.ErrorContains(t, err, `invalid telemetry.attributes entry "nope": expected key=value`)
}

// TestRunLeavesTelemetryAsItFoundIt is the same guarantee seen from the binary: a run that started
// telemetry and finished has put the process back the way it was.
func TestRunLeavesTelemetryAsItFoundIt(t *testing.T) {
	before := beholder.GetClient()

	require.NoError(t, execute(t, logger.Test(t), fakeConstructor, "run"))

	assert.Same(t, before, beholder.GetClient())
}
