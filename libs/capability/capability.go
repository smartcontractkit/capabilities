package capability

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Capability is what a constructor has to return: something that runs, that can be called as a
// capability, and that can say which methods it has.
//
// It is declared here rather than taken from standalone/capability, whose Capability is the same
// three things, because that package reaches back into the bootstrapper this one is meant to
// replace - and importing it would make this package depend on what it is replacing. Go interfaces
// are structural, so nothing is duplicated in practice: a value satisfying one satisfies the other,
// and a generated server satisfies both without being told about either.
type Capability interface {
	services.Service
	capabilities.ExecutableAndTriggerCapability

	// Service is the proto service this capability's server was generated from.
	//
	// The untyped capability API says nothing about the methods behind it: a request carries a
	// method name and an opaque payload, and what shapes that payload is the proto the server was
	// generated from. Handing the descriptor back is what lets something outside the capability -
	// the debug UI, say - know which methods exist and what each one takes, without the capability
	// having to describe itself twice.
	//
	// The generated server implements this, so a capability gets it for free.
	Service() protoreflect.ServiceDescriptor
}

// Config marks the struct a constructor asks for as the capability's own configuration - the
// one parameter an operator supplies rather than the run resolves:
//
//	type Config struct {
//		capability.Config
//		FastestScheduleIntervalSeconds int `usage:"fastest cron schedule a workflow may register"`
//	}
//
// The run binds the struct's fields as flags on the root command, namespaced by the binary's
// name - cron's are --cron.fastest-schedule-interval-seconds, CRE_CRON_FASTEST_SCHEDULE_INTERVAL_SECONDS,
// or the same key in the config file - and hands the constructor the decoded value. A constructor
// declares at most one.
//
// The marker, rather than treating any struct parameter as config, keeps a struct named by
// accident an error instead of a silent new flag namespace.
type Config struct{}

// capabilityConfig is what a capability binary needs from the node it runs beside.
type capabilityConfig struct {
	// ProxyURL is a grpc.NewClient target rather than a port, so the registry proxy is not assumed
	// to be a process on this machine. The node usually runs it beside this one ("localhost:9000"),
	// but a shared proxy, a sidecar on another host or a DNS name behind several of them are all
	// just a different target - and none of them can be expressed as a port.
	ProxyURL        string `usage:"gRPC target of the node's capability registry proxy (e.g. localhost:9000, or dns:///registry.internal:9000), used to resolve capabilities this binary does not host. Unset resolves only the capabilities this binary hosts"`
	CapabilityDonID uint32 `usage:"on-chain DON ID of the capability DON this process was spawned for"`

	// HTTPDebug serves the debug UI on the shared HTTP server. Off by default: it invokes
	// capabilities, so it is something a run opts into rather than something a configured process
	// exposes because it can.
	HTTPDebug bool `usage:"serve the capability debug UI on the shared HTTP server, under /debug/capabilities"`
}

// Dependencies are what a run hands the capability it builds: the things a capability needs that
// its own configuration cannot give it.
//
// A constructor asks for these one at a time rather than taking the struct: what it declares is
// what it uses, so adding a field here does not widen what every capability appears to need. See
// offered.
type Dependencies struct {
	// Logger is this process's, already named after the binary. A capability names its own from
	// it rather than building one, so everything the process logs lands in one stream, in one
	// format, at the level the operator set - and a node reading several capability binaries side
	// by side can tell which one a line came from.
	Logger logger.Logger

	// CapabilityRegistry is how a capability reaches the capabilities it does not host, and where
	// it is registered so that others can reach it.
	//
	// What is behind it depends on the run. With a proxy configured it is the node's registry, and
	// a lookup this binary cannot answer goes there; without one it holds only what this binary
	// registered, and the metadata calls fail rather than answering with something invented.
	CapabilityRegistry core.CapabilitiesRegistry

	// LimitsFactory is how a capability bounds what a workflow may ask of it - how fast a schedule
	// may be, how much it may send - against the CRE settings the node broadcasts.
	//
	// The limits it makes resolve their effective value on each use rather than at creation, so a
	// capability built with this follows a settings reload without being told.
	LimitsFactory limits.Factory
}
