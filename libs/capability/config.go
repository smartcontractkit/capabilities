package capability

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
)

const capabilitiesNamespace = "capabilities"

const grpcNamespace = "grpc"

// the standard configuration for a capability; does not include the config that a capability may request via its constructor.
type config struct {
	observability observability
	capabilities  capabilityConfig
	grpc          grpcConfig
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
	)
}

// bind binds every config to root, each under the namespace that owns it.
func (c *config) bind(root *cobra.Command) error {
	opts := flags.DefaultTOMLOptions("CRE", "CL")
	for _, s := range c.namespaced() {
		opts.Namespace = s.namespace
		if err := flags.RegisterCommandFlags(root, s.target, opts); err != nil {
			return fmt.Errorf("failed to register the %s settings: %w", s.namespace, err)
		}
	}
	return nil
}
