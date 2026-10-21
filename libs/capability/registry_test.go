package capability

import (
	"context"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/remote"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
)

var testGRPC = GRPCConfig{AdvertiseHost: defaultHost}

// TestNewRegistryDoesNotDial is the property the boot sequence rests on: the node starts this
// process and the two race, so a registry proxy that is not up yet has to delay the first lookup
// rather than fail the run.
//
// Nothing is listening on the port below, and building the registry still succeeds.
func TestNewRegistryDoesNotDial(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: "localhost:1", CapabilityDonID: 1}, testGRPC, loop.GRPCOpts{})

	require.NoError(t, err, "an unreachable proxy should not fail construction")
	require.NotNil(t, reg.fallback)
	assert.Empty(t, reg.added, "nothing is added yet, so nothing is announced")

	require.NoError(t, reg.Start(t.Context()))
	require.NoError(t, reg.Close())
}

// TestNewRegistryDoesNotValidateTheTarget pins something worth knowing before debugging one: not
// even a nonsense target fails here.
//
// grpc.NewClient defers everything to the first RPC, so a typo in --capabilities.proxy-url is not
// reported at startup - it surfaces as a failed capability lookup later on.
func TestNewRegistryDoesNotValidateTheTarget(t *testing.T) {
	for _, target := range []string{"!://not a target", "unknownscheme:///x"} {
		t.Run(target, func(t *testing.T) {
			reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: target}, testGRPC, loop.GRPCOpts{})
			require.NoError(t, err)
			require.NoError(t, reg.Start(t.Context()))
			assert.NoError(t, reg.Close())
		})
	}
}

// TestNewRegistryWithoutAProxyIsLocalOnly covers a binary with no node behind it: it dials nothing,
// and resolves only what it hosts.
//
// The metadata calls failing is the point rather than a shortcoming. A process holding capability
// values has no way to know which DONs exist, so saying so beats answering with something invented.
func TestNewRegistryWithoutAProxyIsLocalOnly(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{CapabilityDonID: 1}, testGRPC, loop.GRPCOpts{})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reg.Close()) })
	require.NoError(t, reg.Start(t.Context()))

	assert.Nil(t, reg.conn, "nothing should have been dialled")
	assert.Nil(t, reg.fallback)

	_, err = reg.DONByID(t.Context(), 1)
	assert.Error(t, err, "there is no node to ask, so the metadata calls should fail")
}

// TestRegistryAddHoldsUntilClose covers the local half: this process's registry resolves what was
// added to it, and stops doing so once it closes.
func TestRegistryAddHoldsUntilClose(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{}, testGRPC, loop.GRPCOpts{})
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))

	require.NoError(t, reg.Add(t.Context(), newFake()))

	got, err := reg.Get(t.Context(), fakeID)
	require.NoError(t, err)
	assert.NotNil(t, got)

	// Remove hollows the registry's entry out rather than deleting it, so what the ID still maps to
	// answers nothing.
	require.NoError(t, reg.Close())

	got, err = reg.Get(t.Context(), fakeID)
	if err == nil {
		_, err = got.Info(t.Context())
	}
	require.Error(t, err, "the registry should no longer hold the capability")
}

// TestRegistryAddAnnouncesToTheNode covers the remote half: with a proxy configured, adding the
// capability announces it to the node, which can reach it back, and closing takes the announcement
// back before the connection it travelled on closes.
func TestRegistryAddAnnouncesToTheNode(t *testing.T) {
	node := newSpyRegistry(t)

	reg, err := newRegistry(logger.Test(t), CapabilitiesConfig{ProxyURL: serveNodeRegistry(t, node)}, testGRPC, loop.GRPCOpts{})
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))

	require.NoError(t, reg.Add(t.Context(), newFake()))

	assert.Equal(t, []string{fakeID}, node.addedIDs(), "the node's registry should know the capability")
	held, err := node.Get(t.Context(), fakeID)
	require.NoError(t, err)
	info, err := held.Info(t.Context())
	require.NoError(t, err, "the node should reach the capability at the address it was announced at")
	assert.Equal(t, fakeID, info.ID)

	require.NoError(t, reg.Close())
	assert.Equal(t, []string{fakeID}, node.removedIDs(), "shutdown should take the announcement back")
}

// spyRegistry is the node's registry, recording what is announced to it and what is taken back.
type spyRegistry struct {
	registry.CapabilitiesRegistry

	mu      sync.Mutex
	adds    []string
	removes []string
}

func newSpyRegistry(t *testing.T) *spyRegistry {
	return &spyRegistry{CapabilitiesRegistry: registry.NewRegistry(logger.Test(t))}
}

func (s *spyRegistry) Add(ctx context.Context, c capabilities.BaseCapability) error {
	info, err := c.Info(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.adds = append(s.adds, info.ID)
	s.mu.Unlock()
	return s.CapabilitiesRegistry.Add(ctx, c)
}

func (s *spyRegistry) Remove(ctx context.Context, id string) error {
	s.mu.Lock()
	s.removes = append(s.removes, id)
	s.mu.Unlock()
	return s.CapabilitiesRegistry.Remove(ctx, id)
}

func (s *spyRegistry) addedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.adds...)
}

func (s *spyRegistry) removedIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.removes...)
}

// serveNodeRegistry serves reg the way the node's registry proxy does, on a free port, and returns
// its address.
func serveNodeRegistry(t *testing.T, reg registry.CapabilitiesRegistry) string {
	t.Helper()
	lggr := logger.Test(t)

	transport, err := remote.NewListenerTransport(remote.ListenerTransportParams{
		Lggr:    lggr,
		Listen:  func() (net.Listener, error) { return net.Listen("tcp", "localhost:0") },
		Connect: func(target string) (*grpc.ClientConn, error) { return dial(target) },
	})
	require.NoError(t, err)

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	srv := grpc.NewServer()
	remote.RegisterCapabilitiesRegistryServer(srv, lggr, reg, transport)
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	return listener.Addr().String()
}
