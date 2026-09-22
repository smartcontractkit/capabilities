package loopserver

import (
	"context"
	"fmt"

	"github.com/cenkalti/backoff/v5"
	"github.com/hashicorp/go-plugin"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"


	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/timeutil"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Deprecated: Use ServeNew instead.
func Serve[T loop.StandardCapabilities](serviceName string, createPluginServer func(logger.Logger) T, opts ...loop.ServerOpt) {
	ServeNew[T](serviceName, func(s *loop.Server) T { return createPluginServer(s.Logger) }, opts...)
}

func ServeNew[T loop.StandardCapabilities](serviceName string, newServer func(*loop.Server) T, opts ...loop.ServerOpt) {
	atomicSettings := loop.NewAtomicSettings(cresettings.DefaultGetter)
	opts = append(opts, loop.WithSettingsGetter(atomicSettings))
	s := loop.MustNewStartedServer(serviceName, opts...)
	defer s.Stop()
	s.Logger.Infof("Starting %s", serviceName)

	stopCh := make(chan struct{})
	defer close(stopCh)

	im, err := newInitMetrics()
	if err != nil {
		s.Logger.Errorw("Failed to create chain capability initialization metrics", "error", err)
	}

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: loop.StandardCapabilitiesHandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			loop.PluginStandardCapabilitiesName: &loop.StandardCapabilitiesLoop{
				PluginServer: &settingsInterceptor{lggr: s.Logger, StandardCapabilities: newServer(s), updateSettings: atomicSettings.Store, initMetrics: im},
				BrokerConfig: loop.BrokerConfig{Logger: s.Logger, StopCh: stopCh, GRPCOpts: s.GRPCOpts},
			},
		},
		GRPCServer: s.GRPCOpts.NewServer,
	})
}

// Deprecated: use ServeNew(serviceName, newServer, loop.WithOtelViews(otelViews))
func ServeNewWithOtelViews[T loop.StandardCapabilities](serviceName string, newServer func(*loop.Server) T, otelViews []sdkmetric.View) {
	ServeNew(serviceName, newServer, loop.WithOtelViews(otelViews))
}

// initMetrics records capability LOOP initialization outcome as gauges,
// labelled by capability, so init failures are visible in monitoring.
type initMetrics struct {
	success otelmetric.Int64Gauge
	failure otelmetric.Int64Gauge
}

func newInitMetrics() (*initMetrics, error) {
	meter := beholder.GetMeter()
	success, err := meter.Int64Gauge(
		"chain_capability_initialization_success",
		otelmetric.WithDescription("1 if the chain capability LOOP initialised successfully, 0 otherwise"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create chain capability initialization success gauge: %w", err)
	}
	failure, err := meter.Int64Gauge(
		"chain_capability_initialization_failure",
		otelmetric.WithDescription("1 if the chain capability LOOP failed to initialise, 0 otherwise"),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create chain capability initialization failure gauge: %w", err)
	}
	return &initMetrics{success: success, failure: failure}, nil
}

func (m *initMetrics) recordInit(ctx context.Context, capability string, err error) {
	if m == nil {
		return
	}
	attrs := otelmetric.WithAttributes(attribute.String("capability", capability))
	if err != nil {
		m.failure.Record(ctx, 1, attrs)
		m.success.Record(ctx, 0, attrs)
		return
	}
	m.success.Record(ctx, 1, attrs)
	m.failure.Record(ctx, 0, attrs)
}

// settingsInterceptor overrides Initialise to intercept CRESettings, if set,
// and record initialization metrics for the wrapped capability.
type settingsInterceptor struct {
	loop.StandardCapabilities
	lggr           logger.Logger
	updateSettings func(settings core.SettingsUpdate) error
	initMetrics    *initMetrics
}

func (i *settingsInterceptor) Initialise(ctx context.Context, deps core.StandardCapabilitiesDependencies) error {
	if deps.CRESettings != nil {
		go func(ctx context.Context) {
			bo := backoff.NewExponentialBackOff()
			for {
				ch, err := deps.CRESettings.Subscribe(ctx)
				if err != nil {
					i.lggr.Errorf("failed to subscribe to settings updates: %v", err)
					if !timeutil.Sleep(ctx.Done(), bo.NextBackOff()) {
						return
					}
					continue // retry
				}
				i.lggr.Info("Subscribed to settings updates")
				bo.Reset()
				for update := range ch {
					if err := i.updateSettings(update); err != nil {
						i.lggr.Errorf("failed to update settings: %v", err)
						continue
					}
					i.lggr.Infow("Updated settings", "hash", update.Hash)
				}
			}
		}(context.WithoutCancel(ctx))
	}
	err := i.StandardCapabilities.Initialise(ctx, deps)
	i.initMetrics.recordInit(ctx, i.lggr.Name(), err)
	return err
}
