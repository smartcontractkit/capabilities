package capability

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAppIDDecodesFromLoopEnv makes sure that this process reads CL_APP_ID, the env var that the core
// node sets for each LOOP.
func TestAppIDDecodesFromLoopEnv(t *testing.T) {
	const appID = "0b9d8c1e-6f3a-4c2b-9e57-2a4d1f8b7c60"
	t.Setenv("CL_APP_ID", appID)

	assert.Equal(t, appID, decoded(t).observability.AppID)
}

// TestPrometheusPortDecodesFromLoopEnv makes sure that this process reads CL_PROMETHEUS_PORT, the
// env var that the core node sets for each LOOP.
func TestPrometheusPortDecodesFromLoopEnv(t *testing.T) {
	t.Setenv("CL_PROMETHEUS_PORT", "9123")

	assert.Equal(t, uint16(9123), decoded(t).observability.PrometheusPort)
}

// TestAppIDDefaultsToARandomID covers a process that the core node did not start. It must make its
// own random ID, and each process must get a different ID.
func TestAppIDDefaultsToARandomID(t *testing.T) {
	first, second := decoded(t).observability.AppID, decoded(t).observability.AppID

	require.NoError(t, uuid.Validate(first))
	assert.NotEqual(t, first, second)
}
