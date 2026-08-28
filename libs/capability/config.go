package capability

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/smartcontractkit/chainlink-common/pkg/config/flags"
)

// capabilitiesNamespace is what the settings a capability binary needs from its host are registered
// under.
const capabilitiesNamespace = "capabilities"

// grpcNamespace is what the gRPC server settings are registered under.
const grpcNamespace = "grpc"

// config is the whole of what a binary is configured with: how this process reports on itself, and
// what it needs to be a capability at all.
//
// The sections are siblings rather than one flat struct because they answer to different things.
// Observability is the same for every binary and the same whether or not it hosts anything;
// capabilities is what this one needs from the node it runs beside; grpc is where the
// capabilities it hosts are served.
type config struct {
	observability observability
	capabilities  capabilityConfig
	grpc          grpcConfig
}

// defaultConfig is what the flags are bound to and decoded into, so an unset setting keeps the
// value it is given here.
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

// register binds every config to root, each under the namespace that owns it.
//
// One at a time rather than as one struct, so a setting is named after the thing it configures
// rather than after the command that accepts it: the beholder client's endpoint is
// --telemetry.endpoint, and CRE_TELEMETRY_ENDPOINT or CL_TELEMETRY_ENDPOINT.
func (c *config) register(root *cobra.Command) error {
	opts := flags.DefaultTOMLOptions("CRE", "CL")
	for _, s := range c.namespaced() {
		opts.Namespace = s.namespace
		if err := flags.RegisterCommandFlags(root, s.target, opts); err != nil {
			return fmt.Errorf("failed to register the %s settings: %w", s.namespace, err)
		}
	}
	return nil
}
