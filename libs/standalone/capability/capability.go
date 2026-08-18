package capability

import (
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
}
