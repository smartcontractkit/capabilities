package capability

import (
	"go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
)

const (
	telemetryNamespace   = "telemetry"
	tracingNamespace     = "tracing"
	chipIngressNamespace = "chip-ingress"
	pyroscopeNamespace   = "pyroscope"
	httpNamespace        = "http"
)

// observability is every process-wide observability config, registered together and consumed once
// the command runs and they have been decoded.
type observability struct {
	telemetry   TelemetryConfig
	tracing     TracingConfig
	chipIngress ChipIngressConfig
	pyroscope   PyroscopeConfig
	http        HTTPConfig

	otelViews []metric.View // supplied through capability.Run
}

// defaultObservability takes the beholder client's own defaults: loop.Server builds its config from
// scratch rather than from beholder.DefaultConfig, so a knob left at zero here would be zero there.
func defaultObservability() *observability {
	b := beholder.DefaultConfig()
	return &observability{
		telemetry: TelemetryConfig{
			TraceSampleRatio:          b.TraceSampleRatio,
			TraceCompressor:           b.TraceCompressor,
			EmitterBatchProcessor:     b.EmitterBatchProcessor,
			EmitterExportTimeout:      *commonconfig.MustNewDuration(b.EmitterExportTimeout),
			EmitterExportInterval:     *commonconfig.MustNewDuration(b.EmitterExportInterval),
			EmitterExportMaxBatchSize: b.EmitterExportMaxBatchSize,
			EmitterMaxQueueSize:       b.EmitterMaxQueueSize,
			// Off, unlike beholder's default: logs already reach their destination via stderr.
			LogStreamingEnabled:    false,
			LogLevel:               b.LogLevel,
			LogBatchProcessor:      b.LogBatchProcessor,
			LogExportTimeout:       *commonconfig.MustNewDuration(b.LogExportTimeout),
			LogExportInterval:      *commonconfig.MustNewDuration(b.LogExportInterval),
			LogExportMaxBatchSize:  b.LogExportMaxBatchSize,
			LogMaxQueueSize:        b.LogMaxQueueSize,
			LogCompressor:          b.LogCompressor,
			MetricCompressor:       b.MetricCompressor,
			MetricCardinalityLimit: b.MetricCardinalityLimit,
		},
		tracing: TracingConfig{SamplingRatio: 1},
		chipIngress: ChipIngressConfig{
			BufferSize:         b.ChipIngressBufferSize,
			MaxBatchSize:       b.ChipIngressMaxBatchSize,
			MaxConcurrentSends: b.ChipIngressMaxConcurrentSends,
			SendInterval:       *commonconfig.MustNewDuration(b.ChipIngressSendInterval),
			SendTimeout:        *commonconfig.MustNewDuration(b.ChipIngressSendTimeout),
			DrainTimeout:       *commonconfig.MustNewDuration(b.ChipIngressDrainTimeout),
			MaxGRPCRequestSize: b.ChipIngressMaxGRPCRequestSize,
		},
		http: HTTPConfig{Port: defaultHTTPPort},
	}
}
