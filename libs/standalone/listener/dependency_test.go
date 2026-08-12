package listener

import (
	"net"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
)

func TestDependency(t *testing.T) {
	t.Run("a single-instance run listens on the configured address", func(t *testing.T) {
		// Port 0 so the test binds something it is guaranteed to be able to.
		dep := Dependency("proxy", "127.0.0.1:0")

		lis, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, lis.Close()) })

		assert.Equal(t, "127.0.0.1", lis.Addr().(*net.TCPAddr).IP.String())
	})

	t.Run("each instance listens one port along", func(t *testing.T) {
		// Bind an ephemeral port first, then build addresses relative to it, so the ports this
		// test needs are ones the OS has just told us are free.
		base, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		require.NoError(t, err)
		require.NoError(t, base.Close())
		basePort := base.Addr().(*net.TCPAddr).Port

		template := Dependency("proxy", net.JoinHostPort("127.0.0.1", strconv.Itoa(basePort)))

		for i := range 2 {
			lis, err := template.ForEmbedding(i).Get(t.Context(), standalone.CommonConfig{})
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, lis.Close()) })

			assert.Equal(t, basePort+i, lis.Addr().(*net.TCPAddr).Port)
		}
	})

	t.Run("an unset address is an error naming the flag", func(t *testing.T) {
		_, err := Dependency("proxy", "").Get(t.Context(), standalone.CommonConfig{})
		require.ErrorContains(t, err, "--proxy.listen-address is required")
	})

	t.Run("closing twice is not a failure", func(t *testing.T) {
		lis, err := Dependency("proxy", "127.0.0.1:0").Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)

		// The bootstrapper closes every resolved dependency, and a server handed a listener closes
		// it while stopping; whichever is second must not report a shutdown failure.
		require.NoError(t, lis.Close())
		require.NoError(t, lis.Close())
	})

	t.Run("the same dependency binds one port however many services resolve it", func(t *testing.T) {
		dep := Dependency("proxy", "127.0.0.1:0")

		first, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, first.Close()) })

		second, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)
		assert.Same(t, first, second)
	})
}
