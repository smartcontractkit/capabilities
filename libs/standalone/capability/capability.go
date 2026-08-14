package capability

import (
	"context"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type Capability interface {
	services.Service
	Initialise(ctx context.Context, dependencies *Dependencies) error
	Info(ctx context.Context) (capabilities.CapabilityInfo, error)
}
