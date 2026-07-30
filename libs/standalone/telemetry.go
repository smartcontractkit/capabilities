package standalone

// This file copies the two small pieces of LOOP plugin setup that standalone
// binaries also need — the hclog-compatible logger and the CL_TELEMETRY_* env
// config for beholder — so that they behave the same with or without a plugin
// host, without depending on loop.Server and the full env contract it requires.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Env vars mirroring the loop.EnvConfig telemetry subset, set by the plugin
// host (or by the operator, when running standalone).
const (
	envTelemetryEndpoint      = "CL_TELEMETRY_ENDPOINT"
	envTelemetryInsecureConn  = "CL_TELEMETRY_INSECURE_CONNECTION"
	envTelemetryCACertFile    = "CL_TELEMETRY_CA_CERT_FILE"
	envTelemetryAttribute     = "CL_TELEMETRY_ATTRIBUTE_"
	envTelemetryAuthHeader    = "CL_TELEMETRY_AUTH_HEADER"
	envTelemetryAuthPubKeyHex = "CL_TELEMETRY_AUTH_PUB_KEY_HEX"
)

// Option configures optional Bootstrapper behavior.
type Option func(*settings)

type settings struct {
	otelViews []sdkmetric.View
}

// WithOtelViews sets otel metric views (e.g. histogram bucket boundaries) on
// the beholder client. Views only apply to instruments created after
// NewBootstrapper, since aggregation is fixed when the client is created.
func WithOtelViews(otelViews []sdkmetric.View) Option {
	return func(s *settings) { s.otelViews = append(s.otelViews, otelViews...) }
}

// newLogger returns a logger encoding hclog-compatible JSON on stderr, like a
// LOOP plugin's: a go-plugin host parses and re-levels these entries, while
// standalone they are plain zap JSON logs. Level is Debug because filtering is
// the reader's job (host-side, or the log pipeline).
func newLogger() (logger.Logger, error) {
	return logger.NewWith(func(cfg *zap.Config) {
		cfg.Level.SetLevel(zap.DebugLevel)
		cfg.EncoderConfig.LevelKey = "@level"
		cfg.EncoderConfig.MessageKey = "@message"
		cfg.EncoderConfig.TimeKey = "@timestamp"
		cfg.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02T15:04:05.000000Z07:00")
	})
}

// startTelemetry creates, starts, and installs the process-global beholder
// client from the CL_TELEMETRY_* env vars, so instruments created afterwards
// via beholder.GetMeter report over OTLP. When no endpoint is configured it
// returns nil and the global noop client stays: instruments record nothing.
func startTelemetry(ctx context.Context, otelViews []sdkmetric.View) (*beholder.Client, error) {
	endpoint := os.Getenv(envTelemetryEndpoint)
	if endpoint == "" {
		return nil, nil
	}

	cfg := beholder.DefaultConfig()
	cfg.OtelExporterGRPCEndpoint = endpoint
	cfg.InsecureConnection = false
	if s := os.Getenv(envTelemetryInsecureConn); s != "" {
		var err error
		cfg.InsecureConnection, err = strconv.ParseBool(s)
		if err != nil {
			return nil, fmt.Errorf("failed to parse %s: %w", envTelemetryInsecureConn, err)
		}
	}
	cfg.CACertFile = os.Getenv(envTelemetryCACertFile)
	for k, v := range envMap(envTelemetryAttribute) {
		cfg.ResourceAttributes = append(cfg.ResourceAttributes, attribute.String(k, v))
	}
	cfg.AuthHeaders = envMap(envTelemetryAuthHeader)
	cfg.AuthPublicKeyHex = os.Getenv(envTelemetryAuthPubKeyHex)
	// Logs already reach their destination via stderr (parsed by the plugin
	// host when under one); don't stream them a second time.
	cfg.LogStreamingEnabled = false
	// Per the OTEL specification, histogram buckets must be defined when the
	// client is created, so the views cannot be applied any later than this.
	cfg.MetricViews = otelViews

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

// envMap collects env vars starting with prefix into a map, with the prefix
// stripped from the keys, mirroring how the plugin host encodes map-valued
// config (loop.EnvConfig.AsCmdEnv). Returns nil when none are set.
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
