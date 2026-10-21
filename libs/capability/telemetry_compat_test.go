package capability

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/loop/telemetry"
)

// TestTelemetryConfigRoundTripsThroughLoopEnv is the contract with a LOOP host: the env it sets with
// telemetry.TelemetryConfig.AsCmdEnv decodes into the same config here.
func TestTelemetryConfigRoundTripsThroughLoopEnv(t *testing.T) {
	cardinalityLimit := 1234
	want := telemetry.TelemetryConfig{
		TracingEnabled:         true,
		TracingCollectorTarget: "collector:4317",
		TracingSamplingRatio:   0.5,
		TracingTLSCertPath:     "/certs/trace.pem",
		TracingAttributes:      map[string]string{"region": "us-east"},

		TelemetryEnabled:                   true,
		TelemetryEndpoint:                  "otel:4317",
		TelemetryInsecureConnection:        true,
		TelemetryCACertFile:                "/certs/ca.pem",
		TelemetryAttributes:                beholder.OtelAttributes{"env": "staging"},
		TelemetryTraceSampleRatio:          0.25,
		TelemetryAuthHeaders:               map[string]string{"authorization": "token"},
		TelemetryAuthPubKeyHex:             "abcdef",
		TelemetryAuthHeadersTTL:            5 * time.Minute,
		TelemetryEmitterBatchProcessor:     true,
		TelemetryEmitterExportTimeout:      2 * time.Second,
		TelemetryEmitterExportInterval:     3 * time.Second,
		TelemetryEmitterExportMaxBatchSize: 11,
		TelemetryEmitterMaxQueueSize:       12,
		TelemetryLogStreamingEnabled:       true,
		TelemetryLogLevel:                  zapcore.WarnLevel,
		TelemetryLogBatchProcessor:         true,
		TelemetryLogExportTimeout:          4 * time.Second,
		TelemetryLogExportMaxBatchSize:     13,
		TelemetryLogExportInterval:         5 * time.Second,
		TelemetryLogMaxQueueSize:           14,
		TelemetryTraceCompressor:           "trace-compressor",
		TelemetryMetricCompressor:          "metric-compressor",
		TelemetryMetricExportBatchSize:     15,
		TelemetryMetricCardinalityLimit:    &cardinalityLimit,
		TelemetryMetricViewsDenyAttributes: []string{"event_id", "workflow_execution_id"},
		TelemetryPrometheusBridgeEnabled:   true,
		TelemetryPrometheusBridgePrefixes:  []string{"foo_", "bar_"},
		TelemetryLogCompressor:             "log-compressor",

		ChipIngressEndpoint:              "chip:9000",
		ChipIngressInsecureConnection:    true,
		ChipIngressBatchEmitterEnabled:   true,
		ChipIngressDurableEmitterEnabled: true,
		ChipIngressBufferSize:            16,
		ChipIngressMaxBatchSize:          17,
		ChipIngressMaxConcurrentSends:    18,
		ChipIngressSendInterval:          6 * time.Second,
		ChipIngressSendTimeout:           7 * time.Second,
		ChipIngressDrainTimeout:          8 * time.Second,
		ChipIngressMaxGRPCRequestSize:    19,
	}
	requireNoZeroFields(t, want)

	for _, kv := range want.AsCmdEnv() {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok, "malformed env entry %q", kv)
		t.Setenv(k, v)
	}

	assert.Equal(t, want, decoded(t).observability.TelemetryConfig)
}

// requireNoZeroFields fails on any field left at its zero value, so a field added to
// telemetry.TelemetryConfig can't go untested by defaulting through on both sides.
func requireNoZeroFields(t *testing.T, cfg telemetry.TelemetryConfig) {
	t.Helper()
	v := reflect.ValueOf(cfg)
	for i := range v.NumField() {
		require.False(t, v.Field(i).IsZero(), "%s is unset; give it a non-default value", v.Type().Field(i).Name)
	}
}
