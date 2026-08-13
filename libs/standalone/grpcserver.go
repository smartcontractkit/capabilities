package standalone

import (
	"context"
	"errors"
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// grpcServer serves one instance's shared gRPC server: whatever services registered on
// StandaloneConfig.GRPCServer during construction.
type grpcServer struct {
	lggr     logger.Logger
	server   *grpc.Server
	listener net.Listener
}

// startGRPCServer listens on port and serves server, which already has every service's RPCs
// registered on it from earlier in that instance's construction. Port 0 asks the OS for an
// ephemeral port, which is logged once bound.
func startGRPCServer(ctx context.Context, lggr logger.Logger, port int, server *grpc.Server) (*grpcServer, error) {
	// An explicit listener resolves port 0 before Serve, so the chosen port can be logged.
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %d: %w", port, err)
	}

	g := &grpcServer{lggr: lggr, server: server, listener: listener}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			g.lggr.Errorw("gRPC server stopped", "err", err)
		}
	}()

	lggr.Infow("Serving gRPC", "address", listener.Addr().String())
	return g, nil
}

func (g *grpcServer) Close() error {
	g.server.GracefulStop()
	return nil
}
