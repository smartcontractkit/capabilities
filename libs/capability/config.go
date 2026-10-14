package capability

import (
	"fmt"

	"github.com/spf13/cobra"
)

const capabilitiesNamespace = "capabilities"

const grpcNamespace = "grpc"

const settingsNamespace = "settings"

const defaultHost = "localhost"

type grpcConfig struct {
	AdvertiseHost string `usage:"host the gRPC servers this process opens bind to and are advertised at; empty binds every interface"`
}

// the standard configuration for a capability; does not include the config that a capability may request via its constructor.
type config struct {
	observability observability
	capabilities  capabilityConfig
	grpc          grpcConfig
	settings      settingsConfig
}

func defaultConfig() *config {
	return &config{
		observability: *defaultObservability(),
		grpc:          grpcConfig{AdvertiseHost: defaultHost},
	}
}

// namespaced pairs every config with the namespace it is registered under, in the order the flags
// are registered.
func (c *config) namespaced() []section {
	return append(c.observability.namespaced(),
		section{capabilitiesNamespace, &c.capabilities},
		section{grpcNamespace, &c.grpc},
		section{settingsNamespace, &c.settings},
	)
}

// bind binds every config to root, each under the namespace that owns it.
func (c *config) bind(root *cobra.Command) error {
	for _, s := range c.namespaced() {
		if err := bindFlags(root, s.namespace, s.target); err != nil {
			return fmt.Errorf("failed to register the %s settings: %w", s.namespace, err)
		}
	}
	return nil
}
