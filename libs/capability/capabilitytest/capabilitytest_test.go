package capabilitytest

import (
	"context"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

type fake struct {
	services.Service
	capabilities.ExecutableAndTriggerCapability
	info  capability.InstanceInfo
	clock clockwork.Clock
}

func (*fake) Info(context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo("fake@1.0.0", capabilities.CapabilityTypeCombined, "a fake")
}

func newFake(i do.Injector) (*fake, error) {
	f := &fake{
		info:  do.MustInvoke[capability.InstanceInfo](i),
		clock: do.MustInvoke[clockwork.Clock](i),
	}

	f.Service, _ = services.Config{Name: "Fake"}.NewServiceEngine(do.MustInvoke[logger.Logger](i))
	return f, nil
}

func TestStart(t *testing.T) {
	fakes := Start(t, newFake, 3)

	require.Len(t, fakes, 3)
	for id, f := range fakes {
		assert.Equal(t, capability.InstanceInfo{ID: id, Total: 3}, f.info)
		assert.NoError(t, f.Ready(), "started")
	}
}

func TestStartWithProvider(t *testing.T) {
	first := clockwork.NewFakeClock()
	second := clockwork.NewFakeClock()

	f := Start(t, newFake, 1,
		capability.WithProvider(func(i *capability.Injector) error { return i.ProvideValue[clockwork.Clock](first) }),
		capability.WithProvider(func(i *capability.Injector) error { return i.ProvideValue[clockwork.Clock](second) }),
	)[0]

	assert.Same(t, second, f.clock, "providers override what the run provides, and later ones override earlier ones")
}
