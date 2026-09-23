package loopserver

import (
	"context"

	"github.com/cenkalti/backoff/v5"
	"github.com/hashicorp/go-plugin"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

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

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: loop.StandardCapabilitiesHandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			loop.PluginStandardCapabilitiesName: &loop.StandardCapabilitiesLoop{
				PluginServer: &settingsInterceptor{lggr: s.Logger, StandardCapabilities: newServer(s), updateSettings: atomicSettings.Store},
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

// settingsInterceptor overrides Initialise in order to intercept CRESettings, if set.
type settingsInterceptor struct {
	loop.StandardCapabilities
	lggr           logger.Logger
	updateSettings func(settings core.SettingsUpdate) error
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
	return i.StandardCapabilities.Initialise(ctx, deps)
}
