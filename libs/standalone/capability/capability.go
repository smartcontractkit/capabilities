package capability

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// Capability is what this package hosts: something that runs, and that can be
// called as a capability.
//
// There is no Initialise. A capability is handed what it needs when it is built
// - Run takes the Dependencies, so whatever constructs the capabilities passed
// to it has them too - and the rest of what an Initialise would have done is
// this package's own: registering the capability, serving it, announcing it, and
// taking it back out again. That leaves nothing to ask the capability to do
// between being built and being started, and so no method to ask it with.
type Capability interface {
	services.Service
	capabilities.ExecutableAndTriggerCapability

	// Service is the proto service this capability's server was generated from.
	//
	// The untyped capability API says nothing about the methods behind it: a
	// request carries a method name and an opaque payload, and what shapes that
	// payload is the proto the server was generated from. Handing the descriptor
	// back is what lets something outside the capability - the debug UI, say -
	// know which methods exist and what each one takes, without the capability
	// having to describe itself twice.
	//
	// The generated server implements this, so a capability gets it for free.
	Service() protoreflect.ServiceDescriptor
}
