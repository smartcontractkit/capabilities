package migration

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// fake stands in for a generated capability server. The capability interface is embedded rather than implemented,
// because nothing here calls it.
type fake struct {
	services.Service
	capabilities.ExecutableAndTriggerCapability

	info    capabilities.CapabilityInfo
	started bool
	closed  bool
}

const fakeID = "fake@1.0.0"

func newFake(t *testing.T) *fake {
	info, err := capabilities.NewCapabilityInfo(fakeID, capabilities.CapabilityTypeCombined, "a fake")
	require.NoError(t, err)

	f := &fake{info: info}
	f.Service, _ = services.Config{
		Name:  "FakeCapability",
		Start: func(context.Context) error { f.started = true; return nil },
		Close: func() error { f.closed = true; return nil },
	}.NewServiceEngine(logger.Test(t))
	return f
}

func (f *fake) Info(context.Context) (capabilities.CapabilityInfo, error) { return f.info, nil }

func (f *fake) Methods() []capability.Method { return nil }

// recordingRegistry records what is added to it and removed from it. It rejects what is added when err is set.
type recordingRegistry struct {
	registry.CapabilitiesRegistry
	err     error
	added   []capabilities.BaseCapability
	removed []string
}

func (r *recordingRegistry) Add(_ context.Context, c capabilities.BaseCapability) error {
	if r.err != nil {
		return r.err
	}

	r.added = append(r.added, c)
	return nil
}

func (r *recordingRegistry) Remove(_ context.Context, id string) error {
	r.removed = append(r.removed, id)
	return nil
}

func TestInitialiseServesTheCapability(t *testing.T) {
	f := newFake(t)
	reg := &recordingRegistry{}
	deps := core.StandardCapabilitiesDependencies{CapabilityRegistry: reg, ShardIndex: 3}

	var got core.StandardCapabilitiesDependencies
	s := newStandardCapabilities("fake", func(d core.StandardCapabilitiesDependencies) (capability.Capability, error) {
		got = d
		return f, nil
	})

	_, err := s.Infos(t.Context())
	require.ErrorIs(t, err, errNotInitialised)
	require.ErrorIs(t, s.Ready(), errNotInitialised)
	assert.Equal(t, map[string]error{"fake": errNotInitialised}, s.HealthReport())

	require.NoError(t, s.Initialise(t.Context(), deps))
	assert.Equal(t, deps, got)
	assert.True(t, f.started)
	require.NoError(t, s.Ready())
	assert.Equal(t, []capabilities.BaseCapability{f}, reg.added)

	infos, err := s.Infos(t.Context())
	require.NoError(t, err)
	assert.Equal(t, []capabilities.CapabilityInfo{f.info}, infos)

	require.NoError(t, s.Close())
	assert.True(t, f.closed)
	assert.Equal(t, []string{fakeID}, reg.removed)
}

func TestCloseBeforeInitialise(t *testing.T) {
	s := newStandardCapabilities("fake", func(core.StandardCapabilitiesDependencies) (capability.Capability, error) {
		t.Error("the capability must not be built")
		return newFake(t), nil
	})
	require.NoError(t, s.Close())
}

func TestInitialiseFails(t *testing.T) {
	t.Run("when the constructor fails", func(t *testing.T) {
		s := newStandardCapabilities("fake", func(core.StandardCapabilitiesDependencies) (capability.Capability, error) {
			return nil, errors.New("no schedule")
		})

		deps := core.StandardCapabilitiesDependencies{CapabilityRegistry: &recordingRegistry{}}
		require.ErrorContains(t, s.Initialise(t.Context(), deps), "failed to instantiate capability: no schedule")
	})

	t.Run("when the registry rejects the capability, closing it", func(t *testing.T) {
		f := newFake(t)
		s := newStandardCapabilities("fake", func(core.StandardCapabilitiesDependencies) (capability.Capability, error) {
			return f, nil
		})
		t.Cleanup(func() { require.NoError(t, s.Close()) })

		deps := core.StandardCapabilitiesDependencies{CapabilityRegistry: &recordingRegistry{err: errors.New("rejected")}}
		require.ErrorContains(t, s.Initialise(t.Context(), deps), "rejected")
		assert.True(t, f.closed)
	})
}
