package capability

// This file is the observability half of what the bootstrapper does: the process-wide services
// every binary has, whether or not its capability knows about them.
//
// They are a deliberate copy of standalone/config.go and its neighbours rather than a move.
// standalone imports this package for NewLogger, so this package cannot import standalone back, and
// moving the settings across would mean moving the helpers they rest on with them - a refactor of
// the thing this spike is meant to replace, in order to spike it. The duplicate resolves by
// deletion rather than by merging: when this package is what starts a binary, the ones over there
// go.
//
// The namespaces and field names are the bootstrapper's, so the env vars flags generates are
// exactly the ones a go-plugin host already sets - telemetry.endpoint binds CL_TELEMETRY_ENDPOINT,
// pyroscope.server-address binds CL_PYROSCOPE_SERVER_ADDRESS - and nothing that runs a binary today
// has to change.

import sdkmetric "go.opentelemetry.io/otel/sdk/metric"

const (
	// Namespaces the observability configs are registered under. Also what makes their env var
	// names match the ones the plugin host sets, so they are load-bearing rather than cosmetic.
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

	// otelViews are the metric views the binary supplies rather than anything an operator
	// configures - see WithOtelViews. They sit here because they are part of how this process
	// reports, even though they are the one thing in this struct that is not a setting.
	otelViews []sdkmetric.View
}

// defaultObservability is what the flags are bound to and decoded into, so an unset setting keeps
// the value it is given here.
func defaultObservability() *observability {
	return &observability{
		tracing: TracingConfig{SamplingRatio: 1},
		http:    HTTPConfig{Port: defaultHTTPPort},
	}
}

// section is one config struct and the namespace its keys sit under.
type section struct {
	namespace string
	target    any
}

// namespaced pairs each config with its namespace, in the order the flags are registered.
//
// The targets are pointers because they are what cobra decodes into: the flags are bound to these
// fields, so what is read when the command runs is what was parsed.
func (o *observability) namespaced() []section {
	return []section{
		{telemetryNamespace, &o.telemetry},
		{tracingNamespace, &o.tracing},
		{chipIngressNamespace, &o.chipIngress},
		{pyroscopeNamespace, &o.pyroscope},
		{httpNamespace, &o.http},
	}
}
