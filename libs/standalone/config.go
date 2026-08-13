package standalone

import (
	"fmt"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/config"
)

// This file holds the process-wide observability configuration: telemetry, tracing, chip
// ingress, profiling, and the metrics/health web server.
//
// These used to be read straight from CL_* env vars, which meant they were invisible to the
// generated config docs, unsettable from a config file, and read before cobra had parsed
// anything. They are now ordinary config structs registered through pkg/config/flags like every
// dependency's, so each setting is a flag, a config-file key and an env var at once.
//
// The namespaces and field names below are chosen so the env vars flags generates are exactly
// the ones a go-plugin host (the core node) already sets - telemetry.endpoint binds
// CL_TELEMETRY_ENDPOINT, pyroscope.server-address binds CL_PYROSCOPE_SERVER_ADDRESS, and so on -
// so nothing that runs this binary today has to change.

const (
	// Namespaces the observability configs are registered under. Also what makes their env var
	// names match the ones the plugin host sets, so they are load-bearing rather than cosmetic.
	telemetryNamespace   = "telemetry"
	tracingNamespace     = "tracing"
	chipIngressNamespace = "chip-ingress"
	pyroscopeNamespace   = "pyroscope"
	prometheusNamespace  = "prometheus"

	// Legacy env prefixes for the two map-valued telemetry settings. pkg/config/flags has no
	// map support, so those are []string of key=value pairs here; a host sets one env var per
	// entry instead, and readEnvPairs still picks those up. See TelemetryConfig.Attributes.
	envTelemetryAttributePrefix  = "CL_TELEMETRY_ATTRIBUTE_"
	envTelemetryAuthHeaderPrefix = "CL_TELEMETRY_AUTH_HEADER_"
)

// TelemetryConfig is the beholder client's configuration: where telemetry goes and how it
// authenticates. An empty Endpoint leaves telemetry off, and the global noop client in place, so
// instruments created by services record nothing.
type TelemetryConfig struct {
	Endpoint           string `usage:"OTLP gRPC endpoint telemetry is exported to; telemetry is disabled when unset"`
	InsecureConnection bool   `usage:"export telemetry over an insecure connection"`
	CACertFile         string `usage:"CA certificate file used to verify the telemetry endpoint"`

	// Attributes and AuthHeaders are key=value pairs ("env=staging") rather than maps, since
	// flags cannot bind a map field. Entries from the legacy CL_TELEMETRY_ATTRIBUTE_<KEY> and
	// CL_TELEMETRY_AUTH_HEADER_<KEY> env vars are merged in on top of these, so a plugin host
	// setting one env var per entry keeps working.
	Attributes  []string `usage:"extra telemetry resource attributes, as key=value pairs" example:"['env=staging']"`
	AuthHeaders []string `usage:"telemetry auth headers, as key=value pairs" flagdocs:"noexample"`

	AuthPubKeyHex           string          `usage:"public key the telemetry auth headers are derived from"`
	AuthHeadersTTL          config.Duration `usage:"how long generated telemetry auth headers are valid for"`
	PrometheusBridgeEnabled bool            `usage:"feed metrics registered on the prometheus registry into the telemetry pipeline"`
}

// TracingConfig is the OTLP tracing configuration. Traces go to the telemetry endpoint, so
// Enabled does nothing unless TelemetryConfig.Endpoint is set too.
type TracingConfig struct {
	Enabled       bool    `usage:"export traces to the telemetry endpoint"`
	SamplingRatio float64 `usage:"fraction of traces sampled, from 0 to 1"`
	TLSCertFile   string  `usage:"TLS certificate file used by the trace exporter"`
}

// ChipIngressConfig points the beholder client's chip ingress emitter at an endpoint. Emitting is
// enabled by setting one.
type ChipIngressConfig struct {
	Endpoint           string `usage:"chip ingress gRPC endpoint; the emitter is disabled when unset"`
	InsecureConnection bool   `usage:"connect to chip ingress over an insecure connection"`
}

// PyroscopeConfig configures continuous profiling. An empty ServerAddress leaves profiling off.
type PyroscopeConfig struct {
	ServerAddress string              `usage:"pyroscope server address; profiling is disabled when unset"`
	AuthToken     config.SecretString `usage:"pyroscope auth token" flagdocs:"noexample"`
	Environment   string              `usage:"environment tag attached to profiles"`
}

// PrometheusConfig is the web server serving /metrics, /debug/pprof and the health endpoints.
//
// Every instance serves its own, so under `embed` instance i listens on Port+i and reports that
// instance's health rather than an aggregate. The default keeps the previous behaviour of an
// unset CL_PROMETHEUS_PORT: no server at all.
type PrometheusConfig struct {
	Port int `usage:"port serving /metrics, /debug/pprof, /healthz and /readyz; -1 disables the server, 0 asks the OS for an ephemeral port. Instance i of an embed run listens on this port plus i"`
}

// disabled reports whether no web server should be started.
func (c PrometheusConfig) disabled() bool { return c.Port < 0 }

// portFor is the port instance i serves on: the configured port plus i, so instances in one
// process do not collide over it. An ephemeral port is left as it is - every instance asking the
// OS for one already gets a distinct port, whereas adding to it would name specific low ports
// nobody chose.
func (c PrometheusConfig) portFor(index int) int {
	if c.Port <= 0 {
		return c.Port
	}
	return c.Port + index
}

// observability is every process-wide observability config, registered together by
// NewBootstrapper and consumed once the command runs and they have been decoded.
type observability struct {
	telemetry   TelemetryConfig
	tracing     TracingConfig
	chipIngress ChipIngressConfig
	pyroscope   PyroscopeConfig
	prometheus  PrometheusConfig
}

// defaultObservability is what the flags are bound to and decoded into, so an unset setting keeps
// the value it is given here.
func defaultObservability() observability {
	return observability{
		tracing: TracingConfig{SamplingRatio: 1},
		// Off unless configured, as an unset CL_PROMETHEUS_PORT was before this was a flag.
		prometheus: PrometheusConfig{Port: -1},
	}
}

// namespaced pairs each config with the namespace it is registered under, in the order the flags
// are registered.
func (o *observability) namespaced() []struct {
	namespace string
	target    any
} {
	return []struct {
		namespace string
		target    any
	}{
		{telemetryNamespace, &o.telemetry},
		{tracingNamespace, &o.tracing},
		{chipIngressNamespace, &o.chipIngress},
		{pyroscopeNamespace, &o.pyroscope},
		{prometheusNamespace, &o.prometheus},
	}
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
