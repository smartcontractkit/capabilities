package main

import (
	"errors"
	"fmt"
	"io"
	"math"
	"sync"

	"github.com/smartcontractkit/libocr/commontypes"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// Server implements the BinaryNetworkEndpointProxy gRPC service. It is backed
// by a real libocr BinaryNetworkEndpointFactory (i.e. a running rage peer) and
// exposes it over the network so that an out-of-process client can drive OCR
// message passing without owning the peer.
//
// Each Connect stream corresponds to exactly one BinaryNetworkEndpoint: the
// first message on the stream must be a NewEndpointRequest, after which the
// stream carries SendTo/Broadcast requests up and received messages down.
type Server struct {
	creproxy.UnimplementedBinaryNetworkEndpointProxyServer

	peerFactory   types.BinaryNetworkEndpointFactory
	inboundSizes  sizeRecorder
	outboundSizes sizeRecorder
}

// NewServer returns a Server that serves endpoints created by the given
// factory, typically networking.NewPeer(...).OCR2BinaryNetworkEndpointFactory().
func NewServer(peerFactory types.BinaryNetworkEndpointFactory, metrics *proxyMetrics) *Server {
	return &Server{
		peerFactory:   peerFactory,
		inboundSizes:  metrics.sizes(endpointOCR2, directionInbound),
		outboundSizes: metrics.sizes(endpointOCR2, directionOutbound),
	}
}

func (s *Server) Connect(stream creproxy.BinaryNetworkEndpointProxy_ConnectServer) error {
	var closers []io.Closer
	wg := sync.WaitGroup{}

	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
		wg.Wait()
	}()

	req, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive initial NewEndpointRequest: %w", err)
	}

	newEndpointReq, ok := req.Message.(*creproxy.BinaryNetworkClientRequest_NewEndpoint)
	if !ok {
		return fmt.Errorf("first message must be NewEndpointRequest, got %T", req.Message)
	}

	endpoint, err := s.handleNewEndpoint(newEndpointReq.NewEndpoint)
	if err != nil {
		return fmt.Errorf("failed to create endpoint: %w", err)
	}
	closers = append(closers, endpoint)

	recvChan := endpoint.Receive()

	ctx := stream.Context()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range recvChan {
			pbMsg := &creproxy.BinaryMessageWithSender{
				Msg:    msg.Msg,
				Sender: uint32(msg.Sender),
			}
			s.inboundSizes.record(ctx, len(msg.Msg))
			if err := stream.Send(pbMsg); err != nil {
				return
			}
		}
	}()

	for {
		req, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		switch msg := req.Message.(type) {
		case *creproxy.BinaryNetworkClientRequest_NewEndpoint:
			return fmt.Errorf("NewEndpointRequest not allowed after initial setup")
		case *creproxy.BinaryNetworkClientRequest_SendTo:
			// The wire carries the oracle ID as a uint32 and an OracleID is a uint8, so a value
			// that does not fit is refused rather than converted: converting would silently
			// truncate it into some other oracle's ID and send the message there.
			to, ok := toOracleID(msg.SendTo.ToOracleId)
			if !ok {
				return fmt.Errorf("oracle ID %d is out of range", msg.SendTo.ToOracleId)
			}
			s.outboundSizes.record(ctx, len(msg.SendTo.Payload))
			endpoint.SendTo(msg.SendTo.Payload, to)
		case *creproxy.BinaryNetworkClientRequest_Broadcast:
			s.outboundSizes.record(ctx, len(msg.Broadcast))
			endpoint.Broadcast(msg.Broadcast)
		}
	}
}

func (s *Server) handleNewEndpoint(req *creproxy.NewEndpointRequest) (commontypes.BinaryNetworkEndpoint, error) {
	bootstrappers := make([]commontypes.BootstrapperLocator, len(req.V2Bootstrappers))
	for i, b := range req.V2Bootstrappers {
		bootstrappers[i] = commontypes.BootstrapperLocator{
			PeerID: b.PeerId,
			Addrs:  b.Addrs,
		}
	}

	if len(req.ConfigDigest) != len(types.ConfigDigest{}) {
		return nil, fmt.Errorf("invalid config digest length: got %d, expected %d", len(req.ConfigDigest), len(types.ConfigDigest{}))
	}
	var configDigest types.ConfigDigest
	copy(configDigest[:], req.ConfigDigest)

	endpoint, err := s.peerFactory.NewEndpoint(
		configDigest,
		req.PeerIds,
		bootstrappers,
		int(req.FailureThreshold),
		types.BinaryNetworkEndpointLimits{
			MaxMessageLength:          int(req.Limits.MaxMessageLength),
			MessagesRatePerOracle:     req.Limits.MessagesRatePerOracle,
			MessagesCapacityPerOracle: int(req.Limits.MessagesCapacityPerOracle),
			BytesRatePerOracle:        req.Limits.BytesRatePerOracle,
			BytesCapacityPerOracle:    int(req.Limits.BytesCapacityPerOracle),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create endpoint: %w", err)
	}

	if err := endpoint.Start(); err != nil {
		return nil, fmt.Errorf("failed to start endpoint: %w", err)
	}

	return endpoint, nil
}

// toOracleID narrows a wire oracle ID to libocr's, reporting whether it fits.
//
// Shared by both proxy servers: an OracleID is a uint8, the wire field is a uint32, and nothing
// upstream constrains it, so every conversion has to be able to refuse.
func toOracleID(id uint32) (commontypes.OracleID, bool) {
	if id > math.MaxUint8 {
		return 0, false
	}
	return commontypes.OracleID(id), true
}
