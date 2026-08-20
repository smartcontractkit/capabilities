package capability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	standalonegrpc "github.com/smartcontractkit/capabilities/libs/standalone/grpc"

	common "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// TestEmbeddedSettingsReachEveryInstance is the wiring that decides whether an embedded run is
// configurable at all: the form the embed command binds its flags to is asked for them before any
// instance exists, and the forms that resolve the instances are built later. They have to be reading
// the same settings, or what was typed stays in the first one.
func TestEmbeddedSettingsReachEveryInstance(t *testing.T) {
	d := &dependency{
		lggr:           logger.Test(t),
		servers:        standalonegrpc.FactoryDependency(logger.Test(t)),
		embeddedConfig: &embeddedConfig{CapabilityDonID: defaultEmbeddedDonID},
	}

	// What the embed command binds to, before it knows how many instances there will be.
	bound, ok := d.ForEmbedding(0, 3).Config().(*embeddedConfig)
	require.True(t, ok)

	// Decoded once the command runs, into that same struct.
	bound.CapabilityDonID = 7

	for i := range 3 {
		cfg, ok := d.ForEmbedding(i, 3).Config().(*embeddedConfig)
		require.True(t, ok, "instance %d", i)
		assert.Equal(t, uint32(7), cfg.CapabilityDonID, "instance %d", i)
	}
}

func TestEmbeddedDependencies(t *testing.T) {
	d := &dependency{
		lggr:           logger.Test(t),
		servers:        standalonegrpc.FactoryDependency(logger.Test(t)),
		embeddedConfig: &embeddedConfig{CapabilityDonID: 9},
	}

	deps, err := d.ForEmbedding(2, 4).Get(t.Context(), common.CommonConfig{})
	require.NoError(t, err)

	assert.Equal(t, uint32(9), deps.CapabilityDonID)
	// The registry holds what this binary registers and nothing else: there is no node behind an
	// embedded run to ask for the rest.
	require.NotNil(t, deps.CapabilityRegistry)
	_, err = deps.CapabilityRegistry.Get(t.Context(), "nothing@1.0.0")
	require.Error(t, err)
}
