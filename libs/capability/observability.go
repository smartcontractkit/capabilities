package capability

import (
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/loop/pyroscope"
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
	pyroscope.PyroscopeConfig
	PrometheusConfig
	http HTTPConfig
	AppConfig

	otelViews []metric.View // supplied through capability.Run
}

// PrometheusConfig is here for compatibility with the core node, which sets CL_PROMETHEUS_PORT for
// each LOOP.
type PrometheusConfig struct {
	// PrometheusPort is the port of the web server. The core node sets it, and gets the metrics
	// from it.
	PrometheusPort uint16
}

// AppConfig identifies the node that started this process. It is here for compatibility with the
// core node, which sets CL_APP_ID for each LOOP.
type AppConfig struct {
	// AppID is the ID of the node process that started this process. The node makes a new random ID
	// each time it starts. Traces use it as service.instance.id. If the node does not set it, this
	// process makes its own random ID.
	AppID string
}

func defaultObservability() *observability {
	return &observability{
		TelemetryConfig: defaultTelemetryConfig(),
		http:            HTTPConfig{Port: defaultHTTPPort},
		AppConfig:       AppConfig{AppID: uuid.NewString()},
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
