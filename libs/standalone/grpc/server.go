// Package grpc provides the gRPC servers a standalone CRE binary serves on.
//
// There are two dependencies here, and which one a binary takes says what its
// address means:
//
//   - Dependency: one server at a configured address (--grpc.host, --grpc.port).
//     For a process something else is told to dial - crecore, whose registry and
//     proxy services core reaches at exactly that address, so it cannot be
//     ephemeral.
//   - FactoryDependency: a factory making servers on ephemeral ports. For a
//     process that announces its own addresses rather than being told one, which
//     is what a capability host does: it registers each capability with the
//     address serving it, so the address only has to be knowable, not fixed.
//
// The factory exists because one address cannot serve two capabilities. A
// registry handle is an ID, a type and a callback URL, and of the RPCs reached
// through that URL only Execute carries a capability ID - BaseCapability.Info
// takes an Empty, and the registration calls carry workflow or trigger metadata.
// So the address is what identifies the capability, and a binary hosting several
// needs one server per capability. That is what the LOOP transport does too: it
// serves each capability on its own grpc.Server behind its own go-plugin broker
// connection, and this is the same arrangement without the broker.
package grpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
)

// Server is one gRPC server: bound when it is created, served for as long as it
// runs.
//
// Binding early is what makes an ephemeral port usable. A server on port 0 has no
// address until something listens, and a caller that has to announce its address
// needs it before anything is serving - so the listener is opened by the
// constructor and only Serve waits for Start. It also means a port already in use
// fails while the process is still starting up rather than once it is nominally
// running.
type Server struct {
	services.Service
	eng *services.Engine

	server   *grpc.Server
	listener net.Listener
	started  atomic.Bool
}

// newServer binds address and returns a server for it. address is host:port; port
// 0 asks the OS for a free one, which is logged once bound.
func newServer(ctx context.Context, lggr logger.Logger, address string) (*Server, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	s := &Server{server: grpc.NewServer(), listener: listener}
	s.Service, s.eng = services.Config{
		Name:  "GRPCServer",
		Start: s.start,
		Close: s.close,
	}.NewServiceEngine(lggr)

	s.eng.Infow(fmt.Sprintf("Bound gRPC to port %s", s.Port()), "address", s.Address())
	return s, nil
}

// Registrar is where services register their RPCs. They must all have done so
// before the server starts: Serve does not accept registrations.
func (s *Server) Registrar() grpc.ServiceRegistrar { return s.server }

// Address is the grpc.NewClient target for this server, which is what a caller
// announcing itself hands out. It is the address as bound, so a server on port 0
// reports the port it actually got.
func (s *Server) Address() string { return s.listener.Addr().String() }

// Port is the port this server bound, as a string.
func (s *Server) Port() string {
	if _, port, err := net.SplitHostPort(s.Address()); err == nil {
		return port
	}
	if tcp, ok := s.listener.Addr().(*net.TCPAddr); ok {
		return strconv.Itoa(tcp.Port)
	}
	return "unknown"
}

func (s *Server) start(context.Context) error {
	s.started.Store(true)
	s.eng.Go(func(context.Context) {
		if err := s.server.Serve(s.listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			s.eng.Errorw("gRPC server stopped", "err", err)
		}
	})
	return nil
}

// Close releases the port, whether or not the server ever served on it.
//
// The two cases differ because the listener is opened by the constructor: a
// server that was started is stopped through the service engine, which closes it
// as part of stopping; one that was not has no engine state to unwind and would
// otherwise hold the port until the process exits - which is exactly the case a
// caller hits when it binds a server and then fails before starting it.
func (s *Server) Close() error {
	if s.started.Load() {
		return s.Service.Close()
	}
	s.server.Stop()
	return s.listener.Close()
}

func (s *Server) close() error {
	s.server.GracefulStop()
	return nil
}
