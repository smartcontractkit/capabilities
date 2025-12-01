package api

import (
	"context"

	pb "github.com/smartcontractkit/capabilities/ring/internal/pb"
	"google.golang.org/grpc"
)

// StatusRequest is a request to get the status
type StatusRequest = pb.StatusRequest

// Status is the scaling status response
type Status = pb.Status

// ScalerServer is the interface for implementing the Scaler service
type ScalerServer interface {
	Status(context.Context, *StatusRequest) (*Status, error)
	MustEmbedUnimplementedScalerServer()
}

// UnimplementedScalerServer must be embedded to have forward compatible implementations
type UnimplementedScalerServer struct {
	pb.UnimplementedScalerServer
}

func (UnimplementedScalerServer) MustEmbedUnimplementedScalerServer() {}

// ScalerClient is the client API for Scaler service
type ScalerClient = pb.ScalerClient

// RegisterScalerServer registers a server with the grpc service registrar
func RegisterScalerServer(s grpc.ServiceRegistrar, srv ScalerServer) {
	pb.RegisterScalerServer(s, &scalerServerAdapter{srv: srv})
}

// scalerServerAdapter adapts our public ScalerServer to pb.ScalerServer
type scalerServerAdapter struct {
	pb.UnimplementedScalerServer
	srv ScalerServer
}

func (a *scalerServerAdapter) Status(ctx context.Context, req *pb.StatusRequest) (*pb.Status, error) {
	return a.srv.Status(ctx, req)
}

// NewScalerClient creates a new scaler client
var NewScalerClient = pb.NewScalerClient

