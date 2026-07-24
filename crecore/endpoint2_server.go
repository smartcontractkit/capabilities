package main

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/smartcontractkit/libocr/commontypes"
	ocr2types "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// maxPendingRequestHandles bounds the number of inbound request handles held
// per endpoint connection. Requests are single-use with expiries; if a client
// never responds, the oldest handles are evicted to avoid unbounded growth.
const maxPendingRequestHandles = 8192

// Endpoint2Server implements the Endpoint2Proxy gRPC service (OCR3.1). It is
// backed by a real libocr BinaryNetworkEndpoint2Factory and exposes it over the
// network, analogous to Server for OCR2 endpoints.
//
// Inbound requests carry a stateful libocr RequestHandle that cannot cross the
// wire; the server retains each handle keyed by a request id and the client
// responds with that id.
type Endpoint2Server struct {
	creproxy.UnimplementedEndpoint2ProxyServer

	factory       ocr2types.BinaryNetworkEndpoint2Factory
	inboundSizes  sizeRecorder
	outboundSizes sizeRecorder
}

// NewEndpoint2Server returns an Endpoint2Server serving endpoints created by
// the given factory, typically networking.NewPeer(...).OCR3_1BinaryNetworkEndpointFactory().
func NewEndpoint2Server(factory ocr2types.BinaryNetworkEndpoint2Factory, metrics *proxyMetrics) *Endpoint2Server {
	return &Endpoint2Server{
		factory:       factory,
		inboundSizes:  metrics.sizes(endpointOCR3_1, directionInbound),
		outboundSizes: metrics.sizes(endpointOCR3_1, directionOutbound),
	}
}

func (s *Endpoint2Server) Connect(stream creproxy.Endpoint2Proxy_ConnectServer) error {
	req, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive initial NewEndpoint2Request: %w", err)
	}
	newReq, ok := req.Message.(*creproxy.Endpoint2ClientRequest_NewEndpoint)
	if !ok {
		return fmt.Errorf("first message must be NewEndpoint2Request, got %T", req.Message)
	}

	endpoint, err := s.newEndpoint(newReq.NewEndpoint)
	if err != nil {
		return fmt.Errorf("failed to create endpoint2: %w", err)
	}
	defer func() { _ = endpoint.Close() }()

	c := &endpoint2Conn{stream: stream, handles: map[uint64]ocr2types.RequestHandle{}}

	ctx := stream.Context()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range endpoint.Receive() {
			pb := c.inboundToPB(msg)
			s.inboundSizes.record(ctx, len(pb.Payload))
			if err := c.send(pb); err != nil {
				return
			}
		}
	}()
	defer wg.Wait()

	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch m := req.Message.(type) {
		case *creproxy.Endpoint2ClientRequest_NewEndpoint:
			return fmt.Errorf("NewEndpoint2Request not allowed after initial setup")
		case *creproxy.Endpoint2ClientRequest_SendTo:
			if out, ok := c.pbToOutbound(m.SendTo.Msg); ok {
				s.outboundSizes.record(ctx, len(m.SendTo.Msg.Payload))
				endpoint.SendTo(out, commontypes.OracleID(m.SendTo.ToOracleId))
			}
		case *creproxy.Endpoint2ClientRequest_Broadcast:
			if out, ok := c.pbToOutbound(m.Broadcast.Msg); ok {
				s.outboundSizes.record(ctx, len(m.Broadcast.Msg.Payload))
				endpoint.Broadcast(out)
			}
		}
	}
}

func (s *Endpoint2Server) newEndpoint(req *creproxy.NewEndpoint2Request) (ocr2types.BinaryNetworkEndpoint2, error) {
	if len(req.ConfigDigest) != len(ocr2types.ConfigDigest{}) {
		return nil, fmt.Errorf("invalid config digest length: got %d, expected %d", len(req.ConfigDigest), len(ocr2types.ConfigDigest{}))
	}
	var cd ocr2types.ConfigDigest
	copy(cd[:], req.ConfigDigest)

	bootstrappers := make([]commontypes.BootstrapperLocator, len(req.V2Bootstrappers))
	for i, b := range req.V2Bootstrappers {
		bootstrappers[i] = commontypes.BootstrapperLocator{PeerID: b.PeerId, Addrs: b.Addrs}
	}

	return s.factory.NewEndpoint(cd, req.PeerIds, bootstrappers,
		endpoint2ConfigFromPB(req.DefaultPriorityConfig),
		endpoint2ConfigFromPB(req.LowPriorityConfig),
	)
}

func endpoint2ConfigFromPB(pb *creproxy.Endpoint2Config) ocr2types.BinaryNetworkEndpoint2Config {
	var c ocr2types.BinaryNetworkEndpoint2Config
	if pb == nil {
		return c
	}
	if l := pb.Limits; l != nil {
		c.BinaryNetworkEndpointLimits = ocr2types.BinaryNetworkEndpointLimits{
			MaxMessageLength:          int(l.MaxMessageLength),
			MessagesRatePerOracle:     l.MessagesRatePerOracle,
			MessagesCapacityPerOracle: int(l.MessagesCapacityPerOracle),
			BytesRatePerOracle:        l.BytesRatePerOracle,
			BytesCapacityPerOracle:    int(l.BytesCapacityPerOracle),
		}
	}
	if pb.OverrideIncomingMessageBufferSize != nil {
		v := int(*pb.OverrideIncomingMessageBufferSize)
		c.OverrideIncomingMessageBufferSize = &v
	}
	if pb.OverrideOutgoingMessageBufferSize != nil {
		v := int(*pb.OverrideOutgoingMessageBufferSize)
		c.OverrideOutgoingMessageBufferSize = &v
	}
	return c
}

// endpoint2Conn holds per-connection state: the gRPC stream and the retained
// inbound request handles.
type endpoint2Conn struct {
	stream creproxy.Endpoint2Proxy_ConnectServer

	sendMu sync.Mutex

	mu      sync.Mutex
	handles map[uint64]ocr2types.RequestHandle
	order   []uint64 // insertion order, for bounded eviction
	nextID  uint64
}

func (c *endpoint2Conn) send(m *creproxy.Endpoint2ServerMessage) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(m)
}

func (c *endpoint2Conn) storeHandle(h ocr2types.RequestHandle) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	id := c.nextID
	c.handles[id] = h
	c.order = append(c.order, id)
	if len(c.order) > maxPendingRequestHandles {
		delete(c.handles, c.order[0])
		c.order = c.order[1:]
	}
	return id
}

func (c *endpoint2Conn) popHandle(id uint64) (ocr2types.RequestHandle, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.handles[id]
	if ok {
		delete(c.handles, id)
	}
	return h, ok
}

func (c *endpoint2Conn) inboundToPB(msg ocr2types.InboundBinaryMessageWithSender) *creproxy.Endpoint2ServerMessage {
	out := &creproxy.Endpoint2ServerMessage{Sender: uint32(msg.Sender)}
	switch m := msg.InboundBinaryMessage.(type) {
	case ocr2types.InboundBinaryMessagePlain:
		out.Payload, out.Priority = m.Payload, uint32(m.Priority)
		out.Kind = &creproxy.Endpoint2ServerMessage_Plain{Plain: &creproxy.InboundPlain2{}}
	case ocr2types.InboundBinaryMessageRequest:
		out.Payload, out.Priority = m.Payload, uint32(m.Priority)
		out.Kind = &creproxy.Endpoint2ServerMessage_Request{
			Request: &creproxy.InboundRequest2{RequestId: c.storeHandle(m.RequestHandle)},
		}
	case ocr2types.InboundBinaryMessageResponse:
		out.Payload, out.Priority = m.Payload, uint32(m.Priority)
		out.Kind = &creproxy.Endpoint2ServerMessage_Response{Response: &creproxy.InboundResponse2{}}
	}
	return out
}

func (c *endpoint2Conn) pbToOutbound(msg *creproxy.OutboundMessage2) (ocr2types.OutboundBinaryMessage, bool) {
	if msg == nil {
		return nil, false
	}
	priority := ocr2types.BinaryMessageOutboundPriority(msg.Priority)
	switch k := msg.Kind.(type) {
	case *creproxy.OutboundMessage2_Plain:
		return ocr2types.OutboundBinaryMessagePlain{Payload: msg.Payload, Priority: priority}, true
	case *creproxy.OutboundMessage2_Request:
		return ocr2types.OutboundBinaryMessageRequest{
			ResponsePolicy: ocr2types.SingleUseSizedLimitedResponsePolicy{
				MaxSize:         int(k.Request.PolicyMaxSize),
				ExpiryTimestamp: time.UnixMilli(k.Request.PolicyExpiryUnixMs),
			},
			Payload:  msg.Payload,
			Priority: priority,
		}, true
	case *creproxy.OutboundMessage2_Response:
		h, ok := c.popHandle(k.Response.RequestId)
		if !ok {
			// Handle unknown (evicted or already used): drop the response.
			return nil, false
		}
		return ocr2types.MustMakeOutboundBinaryMessageResponse(h, msg.Payload, priority), true
	default:
		return nil, false
	}
}
