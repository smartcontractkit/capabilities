// Package registrytest serves a registry over an in-memory listener and hands out in-memory
// capability addresses, so tests exercise the real dial-by-address path without binding ports.
package registrytest

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	registrypb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry/pb"

	"github.com/smartcontractkit/capabilities/libs/x/registry"
)

func Serve(t testing.TB, reg *registry.Registry) *grpc.ClientConn {
	t.Helper()

	s := grpc.NewServer()
	registrypb.RegisterCapabilitiesRegistryServer(s, registry.NewServer(reg))

	lis := bufconn.Listen(1 << 20)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)

	conn, err := grpc.NewClient("passthrough:///registry",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial the test registry: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// AddrBook hands out in-memory capability addresses and dials them back, so a
// client's dial-by-address path runs for real without needing ports.
//
// Addresses use the passthrough scheme: gRPC would otherwise parse a bare
// "host:port" as a URI scheme and try to resolve it via DNS.
type AddrBook struct {
	mu        sync.Mutex
	n         int
	listeners map[string]*bufconn.Listener
	dials     map[string]int
}

func NewAddrBook() *AddrBook {
	return &AddrBook{
		listeners: map[string]*bufconn.Listener{},
		dials:     map[string]int{},
	}
}

// Listen registers a new listener and returns it. Its Addr is unique per call,
// so a caller that opens one listener per capability announces distinct
// addresses, exactly as it would with port 0.
func (b *AddrBook) Listen() net.Listener {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.n++
	name := fmt.Sprintf("passthrough:///cap-%d", b.n)
	lis := bufconn.Listen(1 << 20)
	b.listeners[name] = lis
	return &namedListener{Listener: lis, addr: memAddr(name)}
}

// Target returns the address for an explicitly named listener, registering it.
func (b *AddrBook) Target(name string) string { return "passthrough:///" + name }

// Serve registers srv under name and starts serving it, returning the address a
// registry should hand out for it.
func (b *AddrBook) Serve(t testing.TB, name string, srv *grpc.Server) string {
	t.Helper()

	lis := bufconn.Listen(1 << 20)
	target := b.Target(name)

	b.mu.Lock()
	b.listeners[target] = lis
	b.mu.Unlock()

	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	return target
}

// DialOption resolves the addresses this book handed out. Unknown addresses
// fail, so "the registry returned an address nobody serves" is observable rather
// than a hang.
func (b *AddrBook) DialOption() grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, target string) (net.Conn, error) {
		b.mu.Lock()
		// The passthrough resolver strips the scheme before dialing, so accept
		// either form.
		lis, ok := b.listeners[target]
		if !ok {
			lis, ok = b.listeners["passthrough:///"+target]
		}
		b.dials[target]++
		b.mu.Unlock()

		if !ok {
			return nil, fmt.Errorf("nothing serving %s", target)
		}
		return lis.DialContext(ctx)
	})
}

// DialCount reports how many transport dials were made to name, which is how a
// test asserts that connections are reused rather than reopened per call.
func (b *AddrBook) DialCount(name string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.dials[name] + b.dials["passthrough:///"+name]
}

type memAddr string

func (a memAddr) Network() string { return "bufconn" }
func (a memAddr) String() string  { return string(a) }

// namedListener overrides bufconn's fixed Addr so each listener has its own.
type namedListener struct {
	net.Listener
	addr memAddr
}

func (l *namedListener) Addr() net.Addr { return l.addr }
