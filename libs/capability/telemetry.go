package capability

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap/zapcore"

	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
)

const (
	// Legacy env prefixes for the map-valued settings. pkg/config/flags has no map support, so
	// those are []string of key=value pairs here; a host sets one env var per entry instead, and
	// envPairs still picks those up. See TelemetryConfig.Attributes.
	envTelemetryAttributePrefix  = "CL_TELEMETRY_ATTRIBUTE_"
	envTelemetryAuthHeaderPrefix = "CL_TELEMETRY_AUTH_HEADER_"
	envTracingAttributePrefix    = "CL_TRACING_ATTRIBUTE_"
)

// TelemetryConfig is the beholder client's configuration: an empty Endpoint leaves telemetry off, and the global noop client in place, so
// instruments created by services record nothing.
//
// Field names follow loop.EnvConfig's, so each flag's env var is the one a plugin host already sets.
type TelemetryConfig struct {
	// Endpoint is the OTLP gRPC endpoint telemetry is exported to. Telemetry is disabled when unset.
	Endpoint string
	// InsecureConnection exports telemetry over an insecure connection.
	InsecureConnection bool
	// CACertFile is the CA certificate file used to verify the telemetry endpoint.
	CACertFile string
	// AppID identifies this process in traces, as their service.instance.id.
	AppID string

	// Attributes and AuthHeaders are key=value pairs ("env=staging") rather than maps. Entries from
	// the legacy CL_TELEMETRY_ATTRIBUTE_<KEY> and CL_TELEMETRY_AUTH_HEADER_<KEY> env vars are merged
	// in on top of these, so a plugin host setting one env var per entry keeps working.

	// Attributes are extra telemetry resource attributes, as key=value pairs.
	Attributes []string
	// AuthHeaders are telemetry auth headers, as key=value pairs.
	AuthHeaders []string

	// AuthPubKeyHex is the public key the telemetry auth headers are derived from.
	AuthPubKeyHex string
	// AuthHeadersTTL is how long generated telemetry auth headers are valid for.
	AuthHeadersTTL commonconfig.Duration
	// PrometheusBridgeEnabled feeds metrics registered on the prometheus registry into the telemetry
	// pipeline.
	PrometheusBridgeEnabled bool
	// PrometheusBridgePrefixes limits the bridged prometheus metrics to those with these name prefixes.
	PrometheusBridgePrefixes []string

	// TraceSampleRatio is the fraction of traces the beholder client samples, from 0 to 1.
	TraceSampleRatio float64
	// TraceCompressor is the compression used when exporting traces.
	TraceCompressor string

	// EmitterBatchProcessor batches emitted messages rather than exporting each one.
	EmitterBatchProcessor bool
	// EmitterExportTimeout bounds each export of emitted messages.
	EmitterExportTimeout commonconfig.Duration
	// EmitterExportInterval is how often batched messages are exported.
	EmitterExportInterval commonconfig.Duration
	// EmitterExportMaxBatchSize is the most messages exported at once.
	EmitterExportMaxBatchSize int
	// EmitterMaxQueueSize is the most messages queued for export before new ones are dropped.
	EmitterMaxQueueSize int

	// LogStreamingEnabled streams logs to the telemetry endpoint as well as writing them to stderr.
	LogStreamingEnabled bool
	// LogLevel is the lowest level of log streamed.
	LogLevel zapcore.Level
	// LogBatchProcessor batches streamed logs rather than exporting each one.
	LogBatchProcessor bool
	// LogExportTimeout bounds each export of streamed logs.
	LogExportTimeout commonconfig.Duration
	// LogExportInterval is how often batched logs are exported.
	LogExportInterval commonconfig.Duration
	// LogExportMaxBatchSize is the most logs exported at once.
	LogExportMaxBatchSize int
	// LogMaxQueueSize is the most logs queued for export before new ones are dropped.
	LogMaxQueueSize int
	// LogCompressor is the compression used when exporting logs.
	LogCompressor string

	// MetricCompressor is the compression used when exporting metrics.
	MetricCompressor string
	// MetricExportBatchSize is the most data points per metric export. Zero disables batching.
	MetricExportBatchSize int
	// MetricCardinalityLimit is the most attribute sets per metric before overflow aggregation. Zero
	// disables the limit.
	MetricCardinalityLimit int
	// MetricViewsDenyAttributes are attribute keys dropped from every metric.
	MetricViewsDenyAttributes []string
}

// TracingConfig is the OTLP tracing configuration.
type TracingConfig struct {
	// Enabled exports traces.
	Enabled bool
	// CollectorTarget is where traces are exported. Defaults to the telemetry endpoint.
	CollectorTarget string
	// SamplingRatio is the fraction of traces sampled, from 0 to 1.
	SamplingRatio float64
	// TLSCertFile is the TLS certificate file used by the trace exporter.
	TLSCertFile string
	// Attributes are extra trace resource attributes, as key=value pairs. Entries from the legacy
	// CL_TRACING_ATTRIBUTE_<KEY> env vars are merged in on top of these.
	Attributes []string
}

// ChipIngressConfig points the beholder client's chip ingress emitter at an endpoint. Emitting is
// enabled by setting one.
type ChipIngressConfig struct {
	// Endpoint is the chip ingress gRPC endpoint. The emitter is disabled when unset.
	Endpoint string
	// InsecureConnection connects to chip ingress over an insecure connection.
	InsecureConnection bool
	// BatchEmitterEnabled batches events sent to chip ingress rather than sending each one.
	BatchEmitterEnabled bool
	// BufferSize is the most events buffered for sending before new ones are dropped.
	BufferSize uint
	// MaxBatchSize is the most events sent at once.
	MaxBatchSize uint
	// MaxConcurrentSends is the most batches in flight at once.
	MaxConcurrentSends int
	// SendInterval is how often buffered events are sent.
	SendInterval commonconfig.Duration
	// SendTimeout bounds each send.
	SendTimeout commonconfig.Duration
	// DrainTimeout bounds sending what is still buffered on shutdown.
	DrainTimeout commonconfig.Duration
	// MaxGRPCRequestSize is the largest request sent to chip ingress, in bytes.
	MaxGRPCRequestSize int
}

// envPairs merges the key=value pairs of a []string setting with the legacy one-env-var-per-entry
// form a plugin host encodes maps in (loop.EnvConfig.AsCmdEnv): PREFIX_SOME_KEY=value becomes
// SOME_KEY=value. The setting wins on conflict, being the more specific source. Returns nil when
// neither supplies anything.
func envPairs(envPrefix, setting string, pairs []string) (map[string]string, error) {
	fromSetting, err := parsePairs(setting, pairs)
	if err != nil {
		return nil, err
	}

	merged := envMap(envPrefix)
	if merged == nil {
		return fromSetting, nil
	}
	for k, v := range fromSetting {
		merged[k] = v
	}
	return merged, nil
}

// envMap collects env vars starting with prefix into a map, with the prefix stripped from the keys.
// Returns nil when none are set.
func envMap(prefix string) map[string]string {
	var m map[string]string
	for _, env := range os.Environ() {
		if key, value, found := strings.Cut(env, "="); found && strings.HasPrefix(key, prefix) {
			if m == nil {
				m = make(map[string]string)
			}
			m[strings.TrimPrefix(key, prefix)] = value
		}
	}
	return m
}

// parsePairs turns key=value strings into a map, erroring on an entry without a "=". Values may
// themselves contain "=", so only the first one separates.
func parsePairs(setting string, pairs []string) (map[string]string, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	m := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		key, value, found := strings.Cut(pair, "=")
		if !found || key == "" {
			return nil, fmt.Errorf("invalid %s entry %q: expected key=value", setting, pair)
		}
		m[key] = value
	}
	return m, nil
}
