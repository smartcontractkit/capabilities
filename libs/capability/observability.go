package capability

import (
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/loop/telemetry"
)

const (
	pyroscopeNamespace = "pyroscope"
	httpNamespace      = "http"
)

// observability is every process-wide observability config, registered together and consumed once
// the command runs and they have been decoded.
type observability struct {
	telemetry.TelemetryConfig
	pyroscope PyroscopeConfig
	http      HTTPConfig

	otelViews []metric.View // supplied through capability.Run
}

func defaultObservability() *observability {
	return &observability{
		TelemetryConfig: defaultTelemetryConfig(),
		http:            HTTPConfig{Port: defaultHTTPPort},
	}
}

// defaultTelemetryConfig is beholder's defaults, for a binary run without a LOOP host setting every
// field through the environment.
func defaultTelemetryConfig() telemetry.TelemetryConfig {
	d := beholder.DefaultConfig()
	return telemetry.TelemetryConfig{
		TracingSamplingRatio: 1,

		TelemetryTraceSampleRatio:          d.TraceSampleRatio,
		TelemetryEmitterBatchProcessor:     d.EmitterBatchProcessor,
		TelemetryEmitterExportTimeout:      d.EmitterExportTimeout,
		TelemetryEmitterExportInterval:     d.EmitterExportInterval,
		TelemetryEmitterExportMaxBatchSize: d.EmitterExportMaxBatchSize,
		TelemetryEmitterMaxQueueSize:       d.EmitterMaxQueueSize,
		TelemetryLogLevel:                  d.LogLevel,
		TelemetryLogBatchProcessor:         d.LogBatchProcessor,
		TelemetryLogExportTimeout:          d.LogExportTimeout,
		TelemetryLogExportMaxBatchSize:     d.LogExportMaxBatchSize,
		TelemetryLogExportInterval:         d.LogExportInterval,
		TelemetryLogMaxQueueSize:           d.LogMaxQueueSize,
		TelemetryTraceCompressor:           d.TraceCompressor,
		TelemetryMetricCompressor:          d.MetricCompressor,
		TelemetryLogCompressor:             d.LogCompressor,
		TelemetryMetricCardinalityLimit:    &d.MetricCardinalityLimit,

		ChipIngressBatchEmitterEnabled: d.ChipIngressBatchEmitterEnabled,
		ChipIngressBufferSize:          d.ChipIngressBufferSize,
		ChipIngressMaxBatchSize:        d.ChipIngressMaxBatchSize,
		ChipIngressMaxConcurrentSends:  d.ChipIngressMaxConcurrentSends,
		ChipIngressSendInterval:        d.ChipIngressSendInterval,
		ChipIngressSendTimeout:         d.ChipIngressSendTimeout,
		ChipIngressDrainTimeout:        d.ChipIngressDrainTimeout,
		ChipIngressMaxGRPCRequestSize:  d.ChipIngressMaxGRPCRequestSize,
	}
}
