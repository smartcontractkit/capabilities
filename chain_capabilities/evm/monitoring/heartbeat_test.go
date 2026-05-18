package monitoring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewPluginHeartbeat(t *testing.T) {
	t.Parallel()

	hb, err := NewPluginHeartbeat()
	require.NoError(t, err)
	require.NotNil(t, hb)

	ctx := context.Background()
	hb.SetAlive(ctx, 1, true)
	hb.Pulse(ctx, 1)
	hb.SetAlive(ctx, 1, false)
}
