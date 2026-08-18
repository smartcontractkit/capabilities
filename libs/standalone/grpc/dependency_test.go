package grpc

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func TestDependency_BindsConfiguredPort(t *testing.T) {
	d := &dependency{lggr: logger.Test(t), cfg: Config{Host: defaultHost, Port: freePort(t)}}

	srv, err := d.Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	assert.Equal(t, net.JoinHostPort("127.0.0.1", srv.Port()), srv.Address())
	assert.Equal(t, uint16Port(t, srv), d.cfg.Port, "should bind the configured port, not an ephemeral one")
}

// Embedding partitions the port the same way the metrics server does, so
// instances sharing a process do not collide.
func TestDependency_ForEmbeddingOffsetsPort(t *testing.T) {
	base := freePort(t)
	d := &dependency{lggr: logger.Test(t), cfg: Config{Host: defaultHost, Port: base}}

	srv, err := d.ForEmbedding(2).Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Close()) })

	assert.Equal(t, base+2, uint16Port(t, srv))
}

func TestFactory_BindsEphemeralPortAndReportsIt(t *testing.T) {
	fd := &factoryDependency{lggr: logger.Test(t), cfg: FactoryConfig{AdvertiseHost: defaultHost}}
	f, err := fd.Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)

	first, err := f.New(t.Context(), logger.Test(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, first.Close()) })

	second, err := f.New(t.Context(), logger.Test(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, second.Close()) })

	// Port 0 is what it asks for; the address it reports is the one it got, which
	// is the whole point - a caller announcing itself cannot advertise 0.
	assert.NotEqual(t, "0", first.Port())
	assert.NotEqual(t, first.Address(), second.Address(), "each server gets its own port")
	assert.Equal(t, net.JoinHostPort("127.0.0.1", first.Port()), first.Address())
}

func TestFactory_StartPortIncrementsPerServer(t *testing.T) {
	start := freePort(t)
	fd := &factoryDependency{lggr: logger.Test(t), cfg: FactoryConfig{AdvertiseHost: defaultHost, StartPort: start}}
	f, err := fd.Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)

	for i := range uint16(3) {
		srv, err := f.New(t.Context(), logger.Test(t))
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, srv.Close()) })
		assert.Equal(t, start+i, uint16Port(t, srv))
	}
}

// The factory is shared across an embed run, so its ports keep running on rather
// than every instance starting over at StartPort and colliding.
func TestFactory_SharedAcrossEmbeddedInstances(t *testing.T) {
	start := freePort(t)
	// Built the way FactoryDependency builds it, with the config set to what the
	// flags would have decoded into it.
	dep := standalone.OnceBootstrapper[*Factory](&factoryDependency{
		lggr: logger.Test(t),
		cfg:  FactoryConfig{AdvertiseHost: defaultHost, StartPort: start},
	})

	first, err := dep.ForEmbedding(0).Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)
	second, err := dep.ForEmbedding(1).Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)
	assert.Same(t, first, second, "every instance should draw from the one factory")

	a, err := first.New(t.Context(), logger.Test(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.Close()) })
	b, err := second.New(t.Context(), logger.Test(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, b.Close()) })

	assert.Equal(t, start, uint16Port(t, a))
	assert.Equal(t, start+1, uint16Port(t, b), "the second instance continues the run rather than restarting it")
}

// uint16Port is srv's bound port, for comparing against a configured one.
func uint16Port(t *testing.T, srv *Server) uint16 {
	t.Helper()
	addr, ok := srv.listener.Addr().(*net.TCPAddr)
	require.True(t, ok)
	return uint16(addr.Port)
}

// freePort asks the OS for a port and hands it back, so the test configures a
// port that is actually available.
func freePort(t *testing.T) uint16 {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	port := uint16(l.Addr().(*net.TCPAddr).Port)
	require.NoError(t, l.Close())
	return port
}
