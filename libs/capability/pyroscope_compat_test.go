package capability

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/loop/pyroscope"
)

// TestPyroscopeConfigRoundTripsThroughLoopEnv is the contract with a LOOP host: the pyroscope env it
// sets with loop.EnvConfig.AsCmdEnv starts the same profiler here as loop.Server would.
func TestPyroscopeConfigRoundTripsThroughLoopEnv(t *testing.T) {
	want := pyroscope.PyroscopeConfig{
		PyroscopeAuthToken:                 "token",
		PyroscopeServerAddress:             "pyroscope:4040",
		PyroscopeEnvironment:               "staging",
		PyroscopeLinkTracesToProfiles:      true,
		PyroscopePPROFBlockProfileRate:     7,
		PyroscopePPROFMutexProfileFraction: 9,
	}
	requireNoZeroPyroscopeFields(t, want)

	for _, kv := range want.AsCmdEnv() {
		k, v, ok := strings.Cut(kv, "=")
		require.True(t, ok, "malformed env entry %q", kv)
		if strings.HasPrefix(k, "CL_PYROSCOPE_") {
			t.Setenv(k, v)
		}
	}

	assert.Equal(t, want, decoded(t).observability.PyroscopeConfig)
}

// requireNoZeroPyroscopeFields fails on any field but AppName left at its zero value, so a field
// added to pyroscope.Config can't go untested by defaulting through on both sides.
func requireNoZeroPyroscopeFields(t *testing.T, cfg pyroscope.PyroscopeConfig) {
	t.Helper()
	v := reflect.ValueOf(cfg)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		require.False(t, v.Field(i).IsZero(), "%s is unset; give it a non-default value", name)
	}
}
