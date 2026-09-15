package capability

import (
	"reflect"

	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

type Capability interface {
	services.Service
	capabilities.ExecutableAndTriggerCapability

	// Service is the proto service this capability's server was generated from.
	Service() protoreflect.ServiceDescriptor
}

// Config marks the struct a constructor asks for as the capability's own configuration
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
type Config struct{}

// capabilityConfig is what a capability binary needs from the node it runs beside.
type capabilityConfig struct {
	ProxyURL        string `usage:"gRPC target of the node's capability registry proxy (e.g. localhost:9000), used to resolve capabilities this binary does not host. Unset resolves only the capabilities this binary hosts"`
	CapabilityDonID uint32 `usage:"on-chain DON ID of the capability DON this process was spawned for"`

	// Serves the Debug UI
	HTTPDebug bool `usage:"serve the capability debug UI on the shared HTTP server, under /debug/capabilities"`
}

type Dependencies struct {
	Logger             logger.Logger
	CapabilityRegistry core.CapabilitiesRegistry
	LimitsFactory      limits.Factory
}

func (d Dependencies) list() []any {
	return []any{d.Logger, d.CapabilityRegistry, d.LimitsFactory}
}

func (d Dependencies) resolve(want reflect.Type) (reflect.Value, bool) {
	for _, v := range d.list() {
		if v == nil {
			continue
		}
		if got := reflect.TypeOf(v); got.AssignableTo(want) {
			return reflect.ValueOf(v), true
		}
	}
	return reflect.Value{}, false
}
