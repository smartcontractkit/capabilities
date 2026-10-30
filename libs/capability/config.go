package capability

//go:generate go run ./internal/docgen

import (
	"context"
	"errors"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/x/config/cli"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"
)

const capabilitiesNamespace = "capabilities"

const grpcNamespace = "grpc"

const settingsNamespace = "settings"

const defaultHost = "localhost"

// GRPCConfig configures the gRPC clients and servers this process opens.
type GRPCConfig struct {
	// AdvertiseHost is the host the gRPC servers this process opens bind to and are advertised at.
	// Empty binds every interface.
	AdvertiseHost string

	loop.GRPCOptsConfig
}

// the standard configuration for a capability; does not include the config that a capability may request via its constructor.
type config struct {
	appName       string
	observability observability
	capabilities  CapabilitiesConfig
	grpc          GRPCConfig
	settings      SettingsConfig

	// Clones of RunInjector and EmbedInjector, made when the run starts. A run uses only these.
	runInjector   *Injector
	embedInjector *Injector

	lggr  logger.Logger                           // set only by tests, with withLogger
	serve func(context.Context, services.Service) // set only by tests, with withServe
}

func defaultGRPCConfig() GRPCConfig {
	return GRPCConfig{AdvertiseHost: defaultHost}
}

func defaultConfig(appName string) *config {
	return &config{
		appName:       appName,
		observability: *defaultObservability(),
		grpc:          defaultGRPCConfig(),
	}
}

// bind binds every config to root, each under the namespace that owns it. Each is settable by
// flag, CL_ env var, or config file, in that order of precedence, over its defaults.
func (c *config) bind(root *cobra.Command) (*cli.Binder, error) {
	b, err := cli.New(cli.Options{Markup: tomlmarkup.New(), Prefixes: []string{"CL"}})
	if err != nil {
		return nil, err
	}
	return b, errors.Join(
		b.Register(root, &c.observability.TelemetryConfig),
		b.Register(root, &c.observability.PyroscopeConfig),
		b.Register(root, &c.observability.AppConfig),
		b.Register(root, &c.observability.PrometheusConfig),
		b.RegisterInNamespace(root, httpNamespace, &c.observability.http),
		b.RegisterInNamespace(root, capabilitiesNamespace, &c.capabilities),
		b.RegisterInNamespace(root, grpcNamespace, &c.grpc),
		b.RegisterInNamespace(root, settingsNamespace, &c.settings),
	)
}
