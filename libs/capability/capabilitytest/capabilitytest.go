package capabilitytest

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// Start builds and starts instances of the capability ctor builds, closing them when the test ends.
func Start[T capability.Capability](t *testing.T, ctor func(do.Injector) (T, error), instances int, opts ...capability.StartOption) []T {
	t.Helper()
	require.Positive(t, instances, "instances must be positive")

	caps, started, err := capability.StartCapabilities(t.Context(), logger.Test(t), capability.EmbedInjector, instances, ctor, opts...)
	t.Cleanup(func() { require.NoError(t, services.MultiCloser(started).Close()) })
	require.NoError(t, err)

	return caps
}
