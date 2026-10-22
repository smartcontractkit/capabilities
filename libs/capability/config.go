package capability

//go:generate go run ./internal/docgen

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/x/config/cli"
	"github.com/smartcontractkit/chainlink-common/x/config/markup/tomlmarkup"
)

const capabilitiesNamespace = "capabilities"

const grpcNamespace = "grpc"

const settingsNamespace = "settings"

const defaultHost = "localhost"

// GRPCConfig configures the gRPC servers this process opens.
type GRPCConfig struct {
	// AdvertiseHost is the host the gRPC servers this process opens bind to and are advertised at.
	// Empty binds every interface.
	AdvertiseHost string
}

// the standard configuration for a capability; does not include the config that a capability may request via its constructor.
type config struct {
	observability observability
	capabilities  CapabilitiesConfig
	grpc          GRPCConfig
	settings      SettingsConfig
}

func defaultConfig() *config {
	return &config{
		observability: *defaultObservability(),
		grpc:          GRPCConfig{AdvertiseHost: defaultHost},
	}
}

// bind binds every config to root, each under the namespace that owns it. Each is settable by
// flag, CRE_/CL_ env var, or config file, in that order of precedence, over its defaults.
func (c *config) bind(root *cobra.Command) (*cli.Binder, error) {
	b, err := cli.New(cli.Options{Markup: tomlmarkup.New(), Prefixes: []string{"CRE", "CL"}})
	if err != nil {
		return nil, err
	}
	return b, errors.Join(
		b.Register(root, &c.observability.TelemetryConfig),
		b.Register(root, &c.observability.PyroscopeConfig),
		b.RegisterInNamespace(root, httpNamespace, &c.observability.http),
		b.RegisterInNamespace(root, capabilitiesNamespace, &c.capabilities),
		b.RegisterInNamespace(root, grpcNamespace, &c.grpc),
		b.RegisterInNamespace(root, settingsNamespace, &c.settings),
	)
}
