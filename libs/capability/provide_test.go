package capability

import (
	"testing"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/x/config/cli"
)

// TestProvideRegistersForBothCommands covers Provide being ProvideForRun and ProvideForEmbed.
func TestProvideRegistersForBothCommands(t *testing.T) {
	resetRegistrations(t)

	Provide(func(do.Injector) (*dependency, error) { return &dependency{}, nil })

	for _, r := range []*registrations{runRegistrations, embedRegistrations} {
		_, err := do.Invoke[*dependency](r.injector())
		assert.NoError(t, err)
	}
}

// TestProvideForOneCommandOnly covers the other two: each reaches its own command alone.
func TestProvideForOneCommandOnly(t *testing.T) {
	resetRegistrations(t)

	ProvideForRun(func(do.Injector) (*dependency, error) { return &dependency{}, nil })

	_, err := do.Invoke[*dependency](runRegistrations.injector())
	require.NoError(t, err)
	_, err = do.Invoke[*dependency](embedRegistrations.injector())
	require.Error(t, err)
}

// TestProvideTwicePanics covers a type provided twice for one command: a programming error, made
// in init, so it panics there rather than surfacing later.
func TestProvideTwicePanics(t *testing.T) {
	resetRegistrations(t)

	ProvideForRun(func(do.Injector) (*dependency, error) { return &dependency{}, nil })
	assert.Panics(t, func() {
		ProvideForRun(func(do.Injector) (*dependency, error) { return &dependency{}, nil })
	})
	// Another command is another registration.
	assert.NotPanics(t, func() {
		ProvideForEmbed(func(do.Injector) (*dependency, error) { return &dependency{}, nil })
	})
}

// TestProvideConfigTwicePanics covers a namespace registered twice for one command.
func TestProvideConfigTwicePanics(t *testing.T) {
	resetRegistrations(t)

	ProvideConfigForRun("a", testConfig{})
	assert.Panics(t, func() { ProvideConfigForRun("a", HTTPConfig{}) })
}

// TestProvideConfigProvidesTheDefault covers the decoded value before anything decodes it: the
// default, as the injector resolves it.
func TestProvideConfigProvidesTheDefault(t *testing.T) {
	resetRegistrations(t)

	ProvideConfigForRun("a", testConfig{Shout: "default"})

	got, err := do.Invoke[testConfig](runRegistrations.injector())
	require.NoError(t, err)
	assert.Equal(t, "default", got.Shout)
}

// TestRunProvidesTheBinder covers the binder in each instance's injector, so a dependency can bind
// further config of its own.
func TestRunProvidesTheBinder(t *testing.T) {
	var got *cli.Binder

	require.NoError(t, execute(t, logger.Test(t), provides(func(i do.Injector) (f *fake, err error) {
		got, err = do.Invoke[*cli.Binder](i)
		return newFake(), err
	}), nil, "run"))

	assert.NotNil(t, got)
}
