package capability

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"

	registrypb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// TestNewRegistryDoesNotDial is the property the boot sequence rests on: the node starts this
// process and the two race, so a registry proxy that is not up yet has to delay the first lookup
// rather than fail the run.
//
// Nothing is listening on the port below, and building the registry still succeeds.
func TestNewRegistryDoesNotDial(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), capabilityConfig{ProxyURL: "localhost:1", CapabilityDonID: 1},
		&serverFactory{host: defaultHost})

	require.NoError(t, err, "an unreachable proxy should not fail construction")
	require.NotNil(t, reg.proxy)
	assert.Empty(t, reg.hosted, "nothing is served yet, so nothing is announced")

	require.NoError(t, reg.Start(t.Context()))
	require.NoError(t, reg.Close())
}

// TestNewRegistryDoesNotValidateTheTarget pins something worth knowing before debugging one: not
// even a nonsense target fails here.
//
// grpc.NewClient defers everything to the first RPC, so a typo in --capabilities.proxy-url is not
// reported at startup - it surfaces as a failed capability lookup later on. What catches an unset
// one is the `validate:"required"` tag on the setting, not this.
func TestNewRegistryDoesNotValidateTheTarget(t *testing.T) {
	for _, target := range []string{"", "!://not a target", "unknownscheme:///x"} {
		t.Run(target, func(t *testing.T) {
			reg, err := newRegistry(logger.Test(t), capabilityConfig{ProxyURL: target}, &serverFactory{host: defaultHost})
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
	reg, err := newRegistry(logger.Test(t), capabilityConfig{CapabilityDonID: 1}, &serverFactory{host: defaultHost})
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, reg.Close()) })
	require.NoError(t, reg.Start(t.Context()))

	assert.Nil(t, reg.conn, "nothing should have been dialled")
	require.NotNil(t, reg.proxy)

	_, err = reg.proxy.DONByID(t.Context(), 1)
	assert.Error(t, err, "there is no node to ask, so the metadata calls should fail")
}

// TestRegistryAddServesAndHolds covers the local half of being reachable: the capability is served
// on an address of its own, and this process's registry resolves it.
func TestRegistryAddServesAndHolds(t *testing.T) {
	reg, err := newRegistry(logger.Test(t), capabilityConfig{}, &serverFactory{host: defaultHost})
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))

	require.NoError(t, reg.Add(t.Context(), newFake()))

	// Served: the hosted server's address answers.
	require.Len(t, reg.hosted, 1)
	conn, err := net.DialTimeout("tcp", reg.hosted[0].server.address(), 5*time.Second)
	require.NoError(t, err)
	_ = conn.Close()

	// Held: the registry resolves the capability as a value.
	got, err := reg.proxy.Get(t.Context(), fakeID)
	require.NoError(t, err)
	assert.NotNil(t, got)

	// Close takes both back. Remove hollows the registry's entry out rather than deleting it, so
	// what the ID still maps to answers nothing.
	require.NoError(t, reg.Close())

	got, err = reg.proxy.Get(t.Context(), fakeID)
	if err == nil {
		_, err = got.Info(t.Context())
	}
	require.Error(t, err, "the registry should no longer hold the capability")
}

// TestRegistryAddAnnouncesToTheNode covers the remote half: with a proxy configured, adding the
// capability announces the address it is served at, and closing takes the announcement back before
// the connection it travelled on closes.
func TestRegistryAddAnnouncesToTheNode(t *testing.T) {
	stub := &stubRegistry{adds: map[string]string{}}

	reg, err := newRegistry(logger.Test(t), capabilityConfig{ProxyURL: serveStubRegistry(t, stub)},
		&serverFactory{host: defaultHost})
	require.NoError(t, err)
	require.NoError(t, reg.Start(t.Context()))

	require.NoError(t, reg.Add(t.Context(), newFake()))

	// Announced at the address it is served at, and that address answers.
	require.Len(t, reg.hosted, 1)
	addr, ok := stub.announced(fakeID)
	require.True(t, ok, "the node's registry should know the capability")
	assert.Equal(t, reg.hosted[0].server.address(), addr)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	require.NoError(t, err)
	_ = conn.Close()

	require.NoError(t, reg.Close())
	stub.mu.Lock()
	defer stub.mu.Unlock()
	assert.Contains(t, stub.removes, fakeID, "shutdown should take the announcement back")
}

// stubRegistry is the node's registry, minimally: it records what is announced to it, and what is
// taken back.
type stubRegistry struct {
	registrypb.UnimplementedCapabilitiesRegistryServer

	mu      sync.Mutex
	adds    map[string]string // capability ID -> the address it was announced at
	removes []string
}

func (s *stubRegistry) Add(_ context.Context, req *registrypb.AddRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adds[req.CapabilityId] = req.CallbackUrl
	return &emptypb.Empty{}, nil
}

func (s *stubRegistry) Remove(_ context.Context, req *registrypb.RemoveRequest) (*emptypb.Empty, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removes = append(s.removes, req.CapabilityId)
	return &emptypb.Empty{}, nil
}

func (s *stubRegistry) announced(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	addr, ok := s.adds[id]
	return addr, ok
}

// serveStubRegistry runs a stub registry on a free port and returns its address.
func serveStubRegistry(t *testing.T, stub *stubRegistry) string {
	t.Helper()

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	gsrv := grpc.NewServer()
	registrypb.RegisterCapabilitiesRegistryServer(gsrv, stub)
	go func() { _ = gsrv.Serve(listener) }()
	t.Cleanup(gsrv.Stop)
	return listener.Addr().String()
}
