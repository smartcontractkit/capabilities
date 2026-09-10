package capability

import "go.opentelemetry.io/otel/sdk/metric"

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

func defaultObservability() *observability {
	return &observability{
		tracing: TracingConfig{SamplingRatio: 1},
		http:    HTTPConfig{Port: defaultHTTPPort},
	}
}

type section struct {
	namespace string
	target    any
}

// namespaced pairs each config with its namespace, in the order the flags are registered.
func (o *observability) namespaced() []section {
	return []section{
		{telemetryNamespace, &o.telemetry},
		{tracingNamespace, &o.tracing},
		{chipIngressNamespace, &o.chipIngress},
		{pyroscopeNamespace, &o.pyroscope},
		{httpNamespace, &o.http},
	}
}
