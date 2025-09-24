package loopserver

import (
	"github.com/hashicorp/go-plugin"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

// Deprecated: Use ServeNew instead.
func Serve[T loop.StandardCapabilities](serviceName string, createPluginServer func(logger.Logger) T) {
	ServeNew[T](serviceName, func(s *loop.Server) T { return createPluginServer(s.Logger) })
}

func ServeNew[T loop.StandardCapabilities](serviceName string, newServer func(*loop.Server) T) {
	s := loop.MustNewStartedServer(serviceName)
	defer s.Stop()
	s.Logger.Infof("Starting %s", serviceName)

	stopCh := make(chan struct{})
	defer close(stopCh)

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: loop.StandardCapabilitiesHandshakeConfig(),
		Plugins: map[string]plugin.Plugin{
			loop.PluginStandardCapabilitiesName: &loop.StandardCapabilitiesLoop{
				PluginServer: newServer(s),
				BrokerConfig: loop.BrokerConfig{Logger: s.Logger, StopCh: stopCh, GRPCOpts: s.GRPCOpts},
			},
		},
		GRPCServer: s.GRPCOpts.NewServer,
	})
}
