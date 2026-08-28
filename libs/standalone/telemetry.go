package standalone

// This file copies the pieces of LOOP plugin setup that standalone binaries also need — the
// hclog-compatible logger and beholder telemetry — so that they behave the same with or without a
// plugin host, without depending on loop.Server and the full env contract it requires. Tracing
// reuses loop.TracingConfig's exporter since it's a standalone helper, not part of that contract.
//
// The settings themselves come from the config structs in config.go rather than from env vars
// directly, so they are flags and config-file keys too; see the note there about why their
// generated env var names still match what a plugin host sets.

import (
	"context"
	"fmt"
	"os"
	"strings"

	prombridge "go.opentelemetry.io/contrib/bridges/prometheus"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

// Option configures optional Bootstrapper behavior.
type Option func(*settings)

type settings struct {
	otelViews []sdkmetric.View
}

// WithOtelViews sets otel metric views (e.g. histogram bucket boundaries) on
// the beholder client. Views only apply to instruments created after the
// telemetry client is started, which happens once the command runs and its
// configuration has been decoded.
func WithOtelViews(otelViews []sdkmetric.View) Option {
	return func(s *settings) { s.otelViews = append(s.otelViews, otelViews...) }
}

// startTelemetry creates, starts, and installs the process-global beholder
// client, so instruments created afterwards via beholder.GetMeter report over
// OTLP. When no endpoint is configured it returns nil and the global noop client
// stays: instruments record nothing.
//
// One client serves every instance of an embed run: it is a process-wide export
// pipeline, and which instance recorded a measurement belongs on the instrument's
// attributes rather than in a second exporter.
func startTelemetry(ctx context.Context, o observability, otelViews []sdkmetric.View) (*beholder.Client, error) {
	if o.telemetry.Endpoint == "" {
		return nil, nil
	}

	cfg := beholder.DefaultConfig()
	cfg.OtelExporterGRPCEndpoint = o.telemetry.Endpoint
	cfg.InsecureConnection = o.telemetry.InsecureConnection
	cfg.CACertFile = o.telemetry.CACertFile

	attributes, err := envPairs(envTelemetryAttributePrefix, "telemetry.attributes", o.telemetry.Attributes)
	if err != nil {
		return nil, err
	}
	for k, v := range attributes {
		cfg.ResourceAttributes = append(cfg.ResourceAttributes, attribute.String(k, v))
	}

	cfg.AuthHeaders, err = envPairs(envTelemetryAuthHeaderPrefix, "telemetry.auth-headers", o.telemetry.AuthHeaders)
	if err != nil {
		return nil, err
	}
	cfg.AuthPublicKeyHex = o.telemetry.AuthPubKeyHex
	cfg.AuthHeadersTTL = o.telemetry.AuthHeadersTTL.Duration()

	// Logs already reach their destination via stderr (parsed by the plugin
	// host when under one); don't stream them a second time.
	cfg.LogStreamingEnabled = false
	// Per the OTEL specification, histogram buckets must be defined when the
	// client is created, so the views cannot be applied any later than this.
	cfg.MetricViews = otelViews

	if o.telemetry.PrometheusBridgeEnabled {
		// Feeds metrics already registered on the global prometheus registry
		// (e.g. via promauto, like the health checker's) into the same OTLP
		// pipeline, so they don't need a separate scrape target.
		cfg.MetricProducers = append(cfg.MetricProducers, prombridge.NewMetricProducer())
	}

	cfg.ChipIngressEmitterGRPCEndpoint = o.chipIngress.Endpoint
	cfg.ChipIngressEmitterEnabled = o.chipIngress.Endpoint != ""
	cfg.ChipIngressInsecureConnection = o.chipIngress.InsecureConnection

	if o.tracing.Enabled {
		tracingCfg := loop.TracingConfig{
			Enabled:         true,
			CollectorTarget: o.telemetry.Endpoint,
			SamplingRatio:   o.tracing.SamplingRatio,
			TLSCertPath:     o.tracing.TLSCertFile,
		}
		if cfg.AuthHeaders != nil {
			tracingCfg.AuthHeaders = cfg.AuthHeaders
		}

		exporter, err := tracingCfg.NewSpanExporter()
		if err != nil {
			return nil, fmt.Errorf("failed to setup tracing exporter: %w", err)
		}
		cfg.TraceSpanExporter = exporter
		cfg.TraceSampleRatio = tracingCfg.SamplingRatio
	}

	client, err := beholder.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create beholder client: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start beholder client: %w", err)
	}
	beholder.SetClient(client)
	beholder.SetGlobalOtelProviders()
	return client, nil
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

// envMap collects env vars starting with prefix into a map, with the prefix
// stripped from the keys. Returns nil when none are set.
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
