package standalone

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPConfigPortFor(t *testing.T) {
	cfg := HTTPConfig{Port: 9090}
	assert.Equal(t, uint16(9090), cfg.portFor(0))
	assert.Equal(t, uint16(9092), cfg.portFor(2))
}

func TestParsePairs(t *testing.T) {
	pairs, err := parsePairs("telemetry.attributes", []string{"env=staging", "region=eu-west-1"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"env": "staging", "region": "eu-west-1"}, pairs)

	t.Run("a value may contain =", func(t *testing.T) {
		pairs, err := parsePairs("telemetry.auth-headers", []string{"authorization=Basic dXNlcjpwYXNz=="})
		require.NoError(t, err)
		assert.Equal(t, map[string]string{"authorization": "Basic dXNlcjpwYXNz=="}, pairs)
	})

	t.Run("nothing configured is not an empty map", func(t *testing.T) {
		pairs, err := parsePairs("telemetry.attributes", nil)
		require.NoError(t, err)
		assert.Nil(t, pairs)
	})

	for _, invalid := range []string{"noequals", "=novalue"} {
		t.Run("rejects "+invalid, func(t *testing.T) {
			_, err := parsePairs("telemetry.attributes", []string{invalid})
			require.ErrorContains(t, err, "expected key=value")
		})
	}
}

func TestEnvPairsMergesLegacyEnvVars(t *testing.T) {
	// A plugin host encodes a map as one env var per entry, which is how these settings arrived
	// before they were flags; both sources have to reach the client.
	t.Setenv(envTelemetryAttributePrefix+"from_env", "yes")
	t.Setenv(envTelemetryAttributePrefix+"overridden", "by env")

	pairs, err := envPairs(envTelemetryAttributePrefix, "telemetry.attributes",
		[]string{"from_setting=yes", "overridden=by setting"})
	require.NoError(t, err)

	assert.Equal(t, map[string]string{
		"from_env":     "yes",
		"from_setting": "yes",
		// The setting wins, being the more specific source.
		"overridden": "by setting",
	}, pairs)
}
