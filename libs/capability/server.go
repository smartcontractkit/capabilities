package capability

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// defaultHost is where a server binds and is advertised unless told otherwise.
const defaultHost = "localhost"

// grpcConfig is where the gRPC servers this process opens bind and are advertised.
type grpcConfig struct {
	AdvertiseHost string `usage:"host the gRPC servers this process opens bind to and are advertised at; empty binds every interface"`

	// StartPort is where the factory's ports begin. Zero, the default, means every server asks
	// the OS for a free port instead.
	StartPort uint16 `usage:"first port the gRPC servers this process opens bind to, incrementing per server; 0 asks the OS for a free port for each of them"`
}

type server struct {
	services.Service
	eng *services.Engine

	grpc     *grpc.Server
	listener net.Listener
	started  atomic.Bool
}

// newServer binds address and returns a server for it. address is host:port; port 0 asks the OS
// for a free one, which is logged once bound.
func newServer(ctx context.Context, lggr logger.Logger, address string) (*server, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	s := &server{grpc: grpc.NewServer(), listener: listener}
	s.Service, s.eng = services.Config{
		Name:  "GRPCServer",
		Start: s.start,
		// No Close hook: stopping the server is what releases the Serve goroutine, and the engine
		// waits for its goroutines before it would run the hook - so the stop lives in Close
		// itself, ahead of the handoff.
	}.NewServiceEngine(lggr)

	s.eng.Infow(fmt.Sprintf("Bound gRPC to port %s", s.port()), "address", s.address())
	return s, nil
}

func (s *server) grpcServer() *grpc.Server { return s.grpc }

// address is the grpc.NewClient target for this server, which is what a caller announcing itself
// hands out. It is the address as bound, so a server on port 0 reports the port it actually got.
func (s *server) address() string { return s.listener.Addr().String() }

// port is the port this server bound, as a string.
func (s *server) port() string {
	if _, port, err := net.SplitHostPort(s.address()); err == nil {
		return port
	}
	if tcp, ok := s.listener.Addr().(*net.TCPAddr); ok {
		return strconv.Itoa(tcp.Port)
	}
	return "unknown"
}

func (s *server) start(context.Context) error {
	s.started.Store(true)
	s.eng.Go(func(context.Context) {
		if err := s.grpc.Serve(s.listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			s.eng.Errorw("gRPC server stopped", "err", err)
		}
	})
	return nil
}

// Close stops serving and releases the port, whether or not the server ever started.
//
// The stop comes before the handoff to the engine, not in its Close hook, because the engine
// closes in the other order: it waits for its goroutines before running the hook, and the Serve
// goroutine returns only once the server is stopped. Stopping from the hook would deadlock the
// two against each other.
//
// The two cases differ because the listener is opened by the constructor: a server that was
// started has the engine to unwind once serving has stopped; one that was not has no engine state
// at all, and would otherwise hold the port until the process exits - which is exactly the case a
// caller hits when it binds a server and then fails before starting it.
func (s *server) Close() error {
	if s.started.Load() {
		s.grpc.GracefulStop()
		return s.Service.Close()
	}
	s.grpc.Stop()
	return s.listener.Close()
}

// serverFactory makes gRPC servers, one per thing that has to be told apart by address - see the
// file comment. Each call binds a port immediately, so the caller can announce the address before
// anything is serving on it.
//
// One factory hands out one run of ports: the ports are the process's, so a second counter would
// have two servers try to bind the same one.
type serverFactory struct {
	host      string
	startPort uint16

	mu     sync.Mutex
	opened uint16 // servers made so far, which is the offset from startPort
}

// new returns a server bound to the next port on the configured host. lggr names it, and the
// caller names it after whatever it serves, so a process with several says which is which.
func (f *serverFactory) new(ctx context.Context, lggr logger.Logger) (*server, error) {
	return newServer(ctx, lggr, net.JoinHostPort(f.host, strconv.Itoa(int(f.nextPort()))))
}

// nextPort is the port the next server binds.
//
// Zero is not a port but a request for any free one, so it is handed out as-is however many
// servers ask: incrementing it would turn "any port" into a deliberate 1, 2, 3, which are neither
// free nor wanted.
func (f *serverFactory) nextPort() uint16 {
	if f.startPort == 0 {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	port := f.startPort + f.opened
	f.opened++
	return port
}
