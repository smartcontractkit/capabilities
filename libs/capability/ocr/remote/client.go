package remote

import (
	"context"
	"errors"
	"fmt"
	"sync"

	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/smartcontractkit/libocr/commontypes"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/libs/capability/ocr/remote/internal/pb"
)

// MaxMessageSize is the largest gRPC message between a node and a process: an OCR message as large as libocr allows,
// with room for its framing. gRPC's 4 MiB default would break an endpoint's stream on a large message, such as a
// leader's proposal carrying many observations; the endpoint on the node still enforces the oracle's own limits. The
// server [RegisterServer] serves on must receive messages this large.
const MaxMessageSize = ragetypes.MaxMessageLength + 1024

// NewClient is the BinaryNetworkEndpointFactory served by RegisterServer on conn, for the node whose peer
// is peerID.
func NewClient(lggr logger.Logger, conn grpc.ClientConnInterface, peerID string) ocrtypes.BinaryNetworkEndpointFactory {
	return &endpointFactory{lggr: logger.Named(lggr, "OCRNetworkClient"), client: pb.NewNetworkClient(conn), peerID: peerID}
}

// outboundBufferSize is how many messages an endpoint holds for the host before dropping the oldest.
const outboundBufferSize = 1024

var _ ocrtypes.BinaryNetworkEndpointFactory = (*endpointFactory)(nil)

type endpointFactory struct {
	lggr   logger.Logger
	client pb.NetworkClient
	peerID string
}

func (f *endpointFactory) PeerID() string { return f.peerID }

func (f *endpointFactory) NewEndpoint(digest ocrtypes.ConfigDigest, peerIDs []string,
	bootstrappers []commontypes.BootstrapperLocator, failureThreshold int,
	limits ocrtypes.BinaryNetworkEndpointLimits) (commontypes.BinaryNetworkEndpoint, error) {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := f.client.Connect(ctx, grpc.MaxCallRecvMsgSize(MaxMessageSize))
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect endpoint: %w", err)
	}

	err = stream.Send(&pb.EndpointRequest{Request: &pb.EndpointRequest_Open{Open: &pb.OpenRequest{
		ConfigDigest:  digest[:],
		PeerIds:       peerIDs,
		Bootstrappers: bootstrappersToPB(bootstrappers),
		F:             int64(failureThreshold),
		Limits:        limitsToPB(limits),
	}}})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open endpoint: %w", err)
	}

	reply, err := stream.Recv()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to open endpoint: %w", err)
	}

	if reply.GetOpened() == nil {
		cancel()
		return nil, fmt.Errorf("expected the endpoint to be opened, got %T", reply.Reply)
	}

	return &endpoint{
		lggr:     logger.With(f.lggr, "configDigest", digest),
		done:     ctx.Done(),
		cancel:   cancel,
		stream:   stream,
		outbound: make(chan *pb.EndpointRequest, outboundBufferSize),
		received: make(chan commontypes.BinaryMessageWithSender),
	}, nil
}

var _ commontypes.BinaryNetworkEndpoint = (*endpoint)(nil)

// endpoint is a BinaryNetworkEndpoint on the host, for the life of one stream.
type endpoint struct {
	lggr     logger.Logger
	done     <-chan struct{}
	cancel   context.CancelFunc
	stream   pb.Network_ConnectClient
	outbound chan *pb.EndpointRequest
	received chan commontypes.BinaryMessageWithSender

	startOnce sync.Once
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func (e *endpoint) Start() error {
	e.startOnce.Do(func() {
		e.wg.Go(e.sendLoop)
		e.wg.Go(e.receiveLoop)
	})

	return nil
}

func (e *endpoint) Close() error {
	e.closeOnce.Do(func() {
		e.cancel()
		e.wg.Wait()
	})

	return nil
}

func (e *endpoint) SendTo(payload []byte, to commontypes.OracleID) {
	e.enqueue(&pb.EndpointRequest{Request: &pb.EndpointRequest_SendTo{SendTo: &pb.SendToRequest{
		Payload: payload,
		To:      uint32(to),
	}}})
}

func (e *endpoint) Broadcast(payload []byte) {
	e.enqueue(&pb.EndpointRequest{Request: &pb.EndpointRequest_Broadcast{Broadcast: &pb.BroadcastRequest{Payload: payload}}})
}

func (e *endpoint) Receive() <-chan commontypes.BinaryMessageWithSender { return e.received }

func (e *endpoint) enqueue(req *pb.EndpointRequest) {
	for {
		select {
		case e.outbound <- req:
			return
		default:
		}

		select {
		case <-e.outbound:
			e.lggr.Warn("Dropped the oldest outbound OCR message, the buffer is full")
		default:
		}
	}
}

func (e *endpoint) sendLoop() {
	for {
		select {
		case <-e.done:
			return
		case req := <-e.outbound:
			if err := e.stream.Send(req); err != nil {
				e.logStreamErr("Failed to send OCR message", err)
				return
			}
		}
	}
}

func (e *endpoint) receiveLoop() {
	for {
		reply, err := e.stream.Recv()
		if err != nil {
			e.logStreamErr("Failed to receive OCR message", err)
			return
		}

		msg := reply.GetMessage()
		if msg == nil {
			e.lggr.Warnw("Ignoring unexpected reply", "reply", fmt.Sprintf("%T", reply.Reply))
			continue
		}

		select {
		case <-e.done:
			return
		case e.received <- commontypes.BinaryMessageWithSender{Msg: msg.Payload, Sender: commontypes.OracleID(msg.Sender)}: //nolint:gosec // oracle IDs fit in a uint8
		}
	}
}

func (e *endpoint) logStreamErr(msg string, err error) {
	if e.closed() || errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return
	}

	e.lggr.Errorw(msg+", the endpoint is closed", "err", err)
}

func (e *endpoint) closed() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}
