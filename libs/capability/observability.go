package capability

import "go.opentelemetry.io/otel/sdk/metric"

const (
	telemetryNamespace   = "telemetry"
	tracingNamespace     = "tracing"
	chipIngressNamespace = "chip-ingress"
	pyroscopeNamespace   = "pyroscope"
	httpNamespace        = "http"
)

// observability is what the beholder client is built from: its settings, resolved by run, and
// the views Run was given.
type observability struct {
	telemetry   TelemetryConfig
	tracing     TracingConfig
	chipIngress ChipIngressConfig

	otelViews []metric.View // supplied through capability.Run
}
