package capability

import (
	"cmp"
	"fmt"
	"os"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
)

// startLoopServer starts chainlink-common's loop.Server - telemetry, tracing, profiling, the web
// server and the health checker - over this process's config.
//
// loop.Server only reads its config from CL_* env vars, so this is a shim: the decoded config is
// turned into the env a plugin host would have set, through the same AsCmdEnv the host uses, for
// as long as the server takes to read it.
func startLoopServer(name string, cfg *config, settings settings.Getter) (*loop.Server, error) {
	env, err := envConfig(&cfg.observability, cfg.grpc)
	if err != nil {
		return nil, err
	}

	// AsCmdEnv leaves an unset optional key out rather than blank, which would let one from this
	// process's env through: CL_DATABASE_URL would have the server connect to a database this
	// config has no say over. Blanked first, so AsCmdEnv's own value wins where it has one.
	cleared := []string{"CL_DATABASE_URL=", "CL_TELEMETRY_METRIC_VIEWS_DENY_ATTRIBUTES="}
	restore, err := setEnv(append(cleared, env.AsCmdEnv()...))
	if err != nil {
		return nil, err
	}
	defer restore()

	return loop.NewStartedServer(name,
		loop.WithOtelViews(cfg.observability.otelViews),
		loop.WithSettingsGetter(settings))
}

func envConfig(obs *observability, grpcCfg GRPCConfig) (loop.EnvConfig, error) {
	attributes, err := envPairs(envTelemetryAttributePrefix, "telemetry.attributes", obs.telemetry.Attributes)
	if err != nil {
		return loop.EnvConfig{}, err
	}
	authHeaders, err := envPairs(envTelemetryAuthHeaderPrefix, "telemetry.auth-headers", obs.telemetry.AuthHeaders)
	if err != nil {
		return loop.EnvConfig{}, err
	}
	tracingAttributes, err := envPairs(envTracingAttributePrefix, "tracing.attributes", obs.tracing.Attributes)
	if err != nil {
		return loop.EnvConfig{}, err
	}

	t, tr, ci, p := obs.telemetry, obs.tracing, obs.chipIngress, obs.pyroscope
	cardinalityLimit := t.MetricCardinalityLimit
	return loop.EnvConfig{
		AppID: t.AppID,

		GRPCServerMaxRecvMsgSize: grpcCfg.ServerMaxRecvMsgSize,

		PrometheusPort: int(obs.http.Port),

		PyroscopeServerAddress:             p.ServerAddress,
		PyroscopeAuthToken:                 string(p.AuthToken),
		PyroscopeEnvironment:               p.Environment,
		PyroscopeLinkTracesToProfiles:      p.LinkTracesToProfiles,
		PyroscopePPROFBlockProfileRate:     p.PPROFBlockProfileRate,
		PyroscopePPROFMutexProfileFraction: p.PPROFMutexProfileFraction,

		TelemetryEnabled:                   t.Endpoint != "",
		TelemetryEndpoint:                  t.Endpoint,
		TelemetryInsecureConnection:        t.InsecureConnection,
		TelemetryCACertFile:                t.CACertFile,
		TelemetryAttributes:                attributes,
		TelemetryTraceSampleRatio:          t.TraceSampleRatio,
		TelemetryAuthHeaders:               authHeaders,
		TelemetryAuthPubKeyHex:             t.AuthPubKeyHex,
		TelemetryAuthHeadersTTL:            t.AuthHeadersTTL.Duration(),
		TelemetryEmitterBatchProcessor:     t.EmitterBatchProcessor,
		TelemetryEmitterExportTimeout:      t.EmitterExportTimeout.Duration(),
		TelemetryEmitterExportInterval:     t.EmitterExportInterval.Duration(),
		TelemetryEmitterExportMaxBatchSize: t.EmitterExportMaxBatchSize,
		TelemetryEmitterMaxQueueSize:       t.EmitterMaxQueueSize,
		TelemetryLogStreamingEnabled:       t.LogStreamingEnabled,
		TelemetryLogLevel:                  t.LogLevel,
		TelemetryLogBatchProcessor:         t.LogBatchProcessor,
		TelemetryLogExportTimeout:          t.LogExportTimeout.Duration(),
		TelemetryLogExportMaxBatchSize:     t.LogExportMaxBatchSize,
		TelemetryLogExportInterval:         t.LogExportInterval.Duration(),
		TelemetryLogMaxQueueSize:           t.LogMaxQueueSize,
		TelemetryTraceCompressor:           t.TraceCompressor,
		TelemetryMetricCompressor:          t.MetricCompressor,
		TelemetryMetricExportBatchSize:     t.MetricExportBatchSize,
		TelemetryMetricCardinalityLimit:    &cardinalityLimit,
		TelemetryMetricViewsDenyAttributes: t.MetricViewsDenyAttributes,
		TelemetryPrometheusBridgeEnabled:   t.PrometheusBridgeEnabled,
		TelemetryPrometheusBridgePrefixes:  t.PrometheusBridgePrefixes,
		TelemetryLogCompressor:             t.LogCompressor,

		TracingEnabled:         tr.Enabled,
		TracingCollectorTarget: cmp.Or(tr.CollectorTarget, t.Endpoint),
		TracingSamplingRatio:   tr.SamplingRatio,
		TracingTLSCertPath:     tr.TLSCertFile,
		TracingAttributes:      tracingAttributes,

		ChipIngressEndpoint:            ci.Endpoint,
		ChipIngressInsecureConnection:  ci.InsecureConnection,
		ChipIngressBatchEmitterEnabled: ci.BatchEmitterEnabled,
		ChipIngressBufferSize:          ci.BufferSize,
		ChipIngressMaxBatchSize:        ci.MaxBatchSize,
		ChipIngressMaxConcurrentSends:  ci.MaxConcurrentSends,
		ChipIngressSendInterval:        ci.SendInterval.Duration(),
		ChipIngressSendTimeout:         ci.SendTimeout.Duration(),
		ChipIngressDrainTimeout:        ci.DrainTimeout.Duration(),
		ChipIngressMaxGRPCRequestSize:  ci.MaxGRPCRequestSize,
	}, nil
}

// setEnv sets each KEY=value in env, and returns what puts the process's env back as it was.
func setEnv(env []string) (func(), error) {
	type previous struct {
		value string
		set   bool
	}
	restoring := map[string]previous{}
	restore := func() {
		for k, p := range restoring {
			if p.set {
				_ = os.Setenv(k, p.value)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}

	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if _, seen := restoring[k]; !seen {
			value, set := os.LookupEnv(k)
			restoring[k] = previous{value, set}
		}
		if err := os.Setenv(k, v); err != nil {
			restore()
			return nil, fmt.Errorf("failed to set %s: %w", k, err)
		}
	}
	return restore, nil
}

// restoreBeholder puts back the beholder client that was global when it was called. loop.Server
// installs its own and leaves it installed when it stops.
func restoreBeholder() func() {
	previous := beholder.GetClient()
	return func() {
		beholder.SetClient(previous)
		beholder.SetGlobalOtelProviders()
	}
}
