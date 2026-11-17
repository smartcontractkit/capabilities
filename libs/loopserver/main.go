package loopserver

import (
	"context"

	"github.com/hashicorp/go-plugin"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Deprecated: Use ServeNew instead.
func Serve[T loop.StandardCapabilities](serviceName string, createPluginServer func(logger.Logger) T, opts ...loop.ServerOpt) {
	ServeNew[T](serviceName, func(s *loop.Server) T { return createPluginServer(s.Logger) }, opts...)
}

func ServeNew[T loop.StandardCapabilities](serviceName string, newServer func(*loop.Server) T, opts ...loop.ServerOpt) {
	atomicSettings := &settings.Atomic{}
	atomicSettings.SetGetter(cresettings.DefaultGetter)
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
				PluginServer: &settingsInterceptor{lggr: s.Logger, StandardCapabilities: newServer(s), settings: atomicSettings},
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
	lggr     logger.Logger
	settings *settings.Atomic
}

func (i *settingsInterceptor) Initialise(ctx context.Context, deps core.StandardCapabilitiesDependencies) error {
	if deps.CRESettings != nil {
		go settings.Follow(context.WithoutCancel(ctx), i.lggr, deps.CRESettings, i.settings)
	}
	return i.StandardCapabilities.Initialise(ctx, deps)
}
