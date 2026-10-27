package capability

import (
	"errors"
	"testing"

	"github.com/jonboulle/clockwork"
	"github.com/samber/do/v2"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// TestEmbedBuildsEachInstanceFromItsOwnClone covers what embed adds over run: one capability per
// instance, each told which it is, each with a registry of its own.
func TestEmbedBuildsEachInstanceFromItsOwnClone(t *testing.T) {
	var (
		infos      []InstanceInfo
		registries []registry.CapabilitiesRegistry
	)

	require.NoError(t, execute(t, logger.Test(t), func(i do.Injector) (Capability, error) {
		infos = append(infos, do.MustInvoke[InstanceInfo](i))
		registries = append(registries, do.MustInvoke[registry.CapabilitiesRegistry](i))
		return newFakeNamed(do.MustInvoke[logger.Logger](i)), nil
	}, "embed", "3"))

	assert.Equal(t, []InstanceInfo{{0, 3}, {1, 3}, {2, 3}}, infos)
	require.Len(t, registries, 3)
	assert.NotSame(t, registries[0], registries[1], "each instance resolves through a registry of its own")
}

// TestEmbedUsesWhatIsProvidedOnEmbedInjector covers the singleton being what each clone starts from.
func TestEmbedUsesWhatIsProvidedOnEmbedInjector(t *testing.T) {
	type shared struct{}
	want := &shared{}

	var got []*shared
	ctor := func(i do.Injector) (Capability, error) {
		got = append(got, do.MustInvoke[*shared](i))
		return newFakeNamed(do.MustInvoke[logger.Logger](i)), nil
	}

	// Provided on a clone, so nothing the test provides outlives it.
	embedInjector := EmbedInjector
	t.Cleanup(func() { EmbedInjector = embedInjector })
	EmbedInjector = embedInjector.Clone()

	require.NoError(t, EmbedInjector.ProvideValue(want))
	require.NoError(t, execute(t, logger.Test(t), ctor, "embed", "2"))

	assert.Equal(t, []*shared{want, want}, got)
}

func TestRunIsOneInstance(t *testing.T) {
	var info InstanceInfo
	require.NoError(t, execute(t, logger.Test(t), func(i do.Injector) (Capability, error) {
		info = do.MustInvoke[InstanceInfo](i)
		return newFake(), nil
	}, "run"))
	assert.Equal(t, InstanceInfo{ID: 0, Total: 1}, info)
}

// --http.port stops execute waiting for a server that the rejected count never starts.
func TestEmbedRejectsABadInstanceCount(t *testing.T) {
	for _, arg := range []string{"0", "two"} {
		require.ErrorContains(t, execute(t, logger.Test(t), fakeConstructor, "embed", arg, "--http.port", "1"),
			"instances must be a positive integer", arg)
	}
}

// embedFakes embeds total fakes from base, closing what started when the test ends.
func embedFakes(t *testing.T, base *Injector, total int, ctor func(do.Injector) (*fake, error), opts ...StartOption) ([]*fake, error) {
	t.Helper()

	fakes, started, err := StartCapabilities(t.Context(), logger.Test(t), base, total, ctor, opts...)
	t.Cleanup(func() { assert.NoError(t, services.MultiCloser(started).Close()) })
	return fakes, err
}

func TestEmbedWithProvider(t *testing.T) {
	first, second := clockwork.NewFakeClock(), clockwork.NewFakeClock()
	base := NewInjector()

	var clocks []clockwork.Clock
	_, err := embedFakes(t, base, 2, func(i do.Injector) (*fake, error) {
		clocks = append(clocks, do.MustInvoke[clockwork.Clock](i))
		return newFakeNamed(do.MustInvoke[logger.Logger](i)), nil
	},
		WithProvider(func(i *Injector) error { return i.ProvideValue[clockwork.Clock](first) }),
		WithProvider(func(i *Injector) error { return i.ProvideValue[clockwork.Clock](second) }),
	)
	require.NoError(t, err)

	assert.Equal(t, []clockwork.Clock{second, second}, clocks, "a provider overrides StartCapabilities's clock, and a later one wins")
	_, err = do.Invoke[clockwork.Clock](base.Do())
	assert.Error(t, err, "overrides are made on clones, never on base")
}

func TestEmbedReturnsWhatStartedWhenAnOverrideFails(t *testing.T) {
	calls := 0
	_, started, err := StartCapabilities(t.Context(), logger.Test(t), NewInjector(), 2, func(i do.Injector) (*fake, error) {
		return newFakeNamed(do.MustInvoke[logger.Logger](i)), nil
	}, WithProvider(func(*Injector) error {
		if calls++; calls == 2 {
			return errors.New("no clock")
		}
		return nil
	}))
	t.Cleanup(func() { assert.NoError(t, services.MultiCloser(started).Close()) })

	require.ErrorContains(t, err, "no clock")
	assert.Len(t, started, 3, "the first instance's registry and capability, and the second's registry")
}

func TestEmbedRejectsANonPositiveCount(t *testing.T) {
	for _, total := range []int{0, -1} {
		_, err := embedFakes(t, NewInjector(), total, func(do.Injector) (*fake, error) { return newFake(), nil })
		require.ErrorContains(t, err, "instances must be positive", total)
	}
}
