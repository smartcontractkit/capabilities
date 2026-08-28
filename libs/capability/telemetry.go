package capability

// The beholder client, and the two services that are configured separately but exported through it:
// tracing, whose spans go to the telemetry endpoint, and chip ingress, whose emitter is part of the
// same client. Both are settings on the client's config rather than services of their own, which is
// why neither has a start of its own to reverse.

import (
	"fmt"
	"os"
	"strings"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

const (
	// Legacy env prefixes for the two map-valued telemetry settings. pkg/config/flags has no map
	// support, so those are []string of key=value pairs here; a host sets one env var per entry
	// instead, and envPairs still picks those up. See TelemetryConfig.Attributes.
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

	AuthPubKeyHex           string                `usage:"public key the telemetry auth headers are derived from"`
	AuthHeadersTTL          commonconfig.Duration `usage:"how long generated telemetry auth headers are valid for"`
	PrometheusBridgeEnabled bool                  `usage:"feed metrics registered on the prometheus registry into the telemetry pipeline"`
}

// TracingConfig is the OTLP tracing configuration. Traces go to the telemetry endpoint, so Enabled
// does nothing unless TelemetryConfig.Endpoint is set too.
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

// newTelemetry builds the beholder client and the service that owns it. It returns the client too,
// since the health checker mirrors itself through the same meter.
//
// It installs the client as the process's, and has to. A capability creates its instruments while
// it is being constructed, and an OTEL instrument resolves beholder.GetMeter() once, at creation -
// so a capability built before this became the global would hold a noop meter for the life of the
// process, recording nothing and reporting no error. Installing when the service starts would be
// too late: the root does not start until the capability it supervises has been built.
//
// It starts nothing. The service it returns is the root's, and the root is what starts and closes
// it - which is also what puts the global back.
//
// When no endpoint is configured it builds no client and returns a service that does nothing. That
// is not an error: it just means the process falls back to the noop beholder client that is global
// until something replaces it, so instruments resolve against a meter that records nothing.
//
// Tracing and chip ingress are exported through this client, so neither does anything without one
// either.
func newTelemetry(lggr logger.Logger, obs *observability) (*telemetryService, error) {
	if obs.telemetry.Endpoint == "" {
		return noopTelemetry(lggr), nil
	}

	cfg, err := beholderConfig(lggr, obs)
	if err != nil {
		return nil, err
	}

	client, err := beholder.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create beholder client: %w", err)
	}

	return newTelemetryService(lggr, client, installGlobally(client)), nil
}

// telemetryService is the beholder client and the process-global it is installed as, as one service
// - so that everything starting telemetry changes is undone by closing it.
//
// Installing happens when this is built rather than when it starts - see newTelemetry - so what
// closing undoes is something that was already done. The two are not symmetric, and cannot be: a
// capability binds its instruments while it is being constructed, which is before the root that
// starts this exists.
//
// The client is a sub-service rather than something this closes itself, which is what gets the
// order right. Sub-services start before the Start hook and close after the Close hook, so the
// client is running by the time it becomes the process's, and is still open when the globals are
// pointed back at the previous one - a late measurement reaches a live pipeline rather than a
// closed one.
type telemetryService struct {
	services.Service

	// client is the beholder client this owns. The health checker mirrors itself through the same
	// meter, so it has to be reachable from outside - which is the only reason it is a field.
	client *beholder.Client
}

// noopTelemetry is telemetry nobody configured: nothing to start, nothing to close, and no client
// behind it. The process keeps the noop beholder client it already had.
//
// A service rather than a nil, so that the caller has one thing to hand the root whether or not
// telemetry is on - and so a nil of this type never reaches rootService.add, where it would arrive
// as a non-nil services.Service holding a nil pointer.
func noopTelemetry(lggr logger.Logger) *telemetryService {
	t := &telemetryService{}
	t.Service, _ = services.Config{Name: "Telemetry"}.NewServiceEngine(lggr)
	return t
}

func newTelemetryService(lggr logger.Logger, client *beholder.Client, restore func()) *telemetryService {
	t := &telemetryService{client: client}
	t.Service, _ = services.Config{
		Name:  "Telemetry",
		Close: func() error { restore(); return nil },
		NewSubServices: func(logger.Logger) []services.Service {
			return []services.Service{client}
		},
	}.NewServiceEngine(lggr)
	return t
}

// installGlobally makes client the process's beholder client, and returns what puts back the one
// that was there before.
//
// The global is the only lasting change starting telemetry makes - everything else it touches is
// owned by the client itself - so this is where reversing it has to be expressed. The otel
// providers are re-pointed on the way back as well as on the way in, since they are derived from
// whichever client is global and would otherwise keep pointing at the one being taken away.
func installGlobally(client *beholder.Client) func() {
	previous := beholder.GetClient()

	beholder.SetClient(client)
	beholder.SetGlobalOtelProviders()

	return func() {
		beholder.SetClient(previous)
		beholder.SetGlobalOtelProviders()
	}
}

// beholderConfig is the telemetry, tracing and chip ingress settings as the one client takes them.
//
// Three configs and one client because that is what they are: an export pipeline, and two more
// kinds of thing exported over it. Registering them separately is what lets an operator turn
// tracing on without restating where telemetry goes.
func beholderConfig(lggr logger.Logger, obs *observability) (beholder.Config, error) {
	cfg := beholder.DefaultConfig()
	cfg.OtelExporterGRPCEndpoint = obs.telemetry.Endpoint
	cfg.InsecureConnection = obs.telemetry.InsecureConnection
	cfg.CACertFile = obs.telemetry.CACertFile

	attributes, err := envPairs(envTelemetryAttributePrefix, "telemetry.attributes", obs.telemetry.Attributes)
	if err != nil {
		return beholder.Config{}, err
	}
	for k, v := range attributes {
		cfg.ResourceAttributes = append(cfg.ResourceAttributes, attribute.String(k, v))
	}

	cfg.AuthHeaders, err = envPairs(envTelemetryAuthHeaderPrefix, "telemetry.auth-headers", obs.telemetry.AuthHeaders)
	if err != nil {
		return beholder.Config{}, err
	}
	cfg.AuthPublicKeyHex = obs.telemetry.AuthPubKeyHex
	cfg.AuthHeadersTTL = obs.telemetry.AuthHeadersTTL.Duration()

	// Logs already reach their destination via stderr (parsed by the plugin host when under one);
	// don't stream them a second time.
	cfg.LogStreamingEnabled = false

	if obs.telemetry.PrometheusBridgeEnabled {
		// Feeds metrics already registered on the global prometheus registry (e.g. via promauto,
		// like the health checker's) into the same OTLP pipeline, so they don't need a separate
		// scrape target.
		cfg.MetricProducers = append(cfg.MetricProducers, prombridge.NewMetricProducer())
	}

	cfg.ChipIngressEmitterGRPCEndpoint = obs.chipIngress.Endpoint
	cfg.ChipIngressEmitterEnabled = obs.chipIngress.Endpoint != ""
	cfg.ChipIngressInsecureConnection = obs.chipIngress.InsecureConnection

	if obs.tracing.Enabled {
		tracing := loop.TracingConfig{
			Enabled:         true,
			CollectorTarget: obs.telemetry.Endpoint,
			SamplingRatio:   obs.tracing.SamplingRatio,
			TLSCertPath:     obs.tracing.TLSCertFile,
			// Not optional, despite reading like it. The exporter's dialer calls this on every
			// failed dial without checking it is set, so leaving it nil turns an unreachable
			// collector - a network blip, a collector restarting - into a nil dereference on a
			// background goroutine, which takes the process with it.
			OnDialError: func(err error) {
				logger.Sugared(lggr).Errorw("Failed to dial the tracing collector",
					"err", err, "target", obs.telemetry.Endpoint)
			},
		}
		if cfg.AuthHeaders != nil {
			tracing.AuthHeaders = cfg.AuthHeaders
		}

		exporter, err := tracing.NewSpanExporter()
		if err != nil {
			return beholder.Config{}, fmt.Errorf("failed to setup tracing exporter: %w", err)
		}
		cfg.TraceSpanExporter = exporter
		cfg.TraceSampleRatio = tracing.SamplingRatio
	}

	// Per the OTEL specification, histogram buckets must be defined when the client is created, so
	// the views cannot be applied any later than this - which is why they are handed to the binary
	// rather than asked of the capability, whose constructor has not run yet. See WithOtelViews.
	cfg.MetricViews = obs.otelViews

	return cfg, nil
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
