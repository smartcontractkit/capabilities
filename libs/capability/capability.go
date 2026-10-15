package capability

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type Capability interface {
	services.Service
	capabilities.ExecutableAndTriggerCapability

	// Service is the proto service this capability's server was generated from.
	Service() protoreflect.ServiceDescriptor
}

// CapabilitiesConfig is what a capability binary needs from the node it runs beside.
type CapabilitiesConfig struct {
	// ProxyURL is the gRPC target of the node's capability registry proxy (e.g. localhost:9000), used
	// to resolve capabilities this binary does not host. Unset resolves only the capabilities this
	// binary hosts.
	ProxyURL string
	// CapabilityDonID is the on-chain DON ID of the capability DON this process was spawned for.
	CapabilityDonID uint32
	// HTTPDebug serves the capability debug UI on the shared HTTP server, under /debug/capabilities.
	HTTPDebug bool
}
