package capability

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// TestEmbedBuildsEachInstanceFromItsOwnClone covers what embed adds over run: one capability per
// instance, each told which it is, each with a registry of its own.
func TestEmbedBuildsEachInstanceFromItsOwnClone(t *testing.T) {
	var (
		infos      []EmbeddedInfo
		registries []core.CapabilitiesRegistry
	)

	require.NoError(t, execute(t, logger.Test(t), func(i do.Injector) (Capability, error) {
		infos = append(infos, do.MustInvoke[EmbeddedInfo](i))
		registries = append(registries, do.MustInvoke[core.CapabilitiesRegistry](i))
		return newFakeNamed(do.MustInvoke[logger.Logger](i)), nil
	}, "embed", "3"))

	assert.Equal(t, []EmbeddedInfo{{0, 3}, {1, 3}, {2, 3}}, infos)
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

func TestRunIsNotEmbedded(t *testing.T) {
	var err error
	require.NoError(t, execute(t, logger.Test(t), func(i do.Injector) (Capability, error) {
		_, err = do.Invoke[EmbeddedInfo](i)
		return newFake(), nil
	}, "run"))
	assert.Error(t, err, "a run is not an embed instance")
}

// --http.port stops execute waiting for a server that the rejected count never starts.
func TestEmbedRejectsABadInstanceCount(t *testing.T) {
	for _, arg := range []string{"0", "two"} {
		require.ErrorContains(t, execute(t, logger.Test(t), fakeConstructor, "embed", arg, "--http.port", "1"),
			"instances must be a positive integer", arg)
	}
}
