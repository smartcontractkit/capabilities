package main

import (
	"fmt"
	"io"
	"sync"

	creproxy "github.com/smartcontractkit/chainlink-protos/cre/impl/proxy"
)

// configDigestLen is the byte length of an OCR config digest.
const configDigestLen = 32

// PeerGroupServer implements the PeerGroupProxy gRPC service. It is backed by a
// creproxy.PeerGroupFactory (the proxy adapts a real libocr
// networking.PeerGroupFactory to this interface) and exposes DON-to-DON peer
// groups and their streams over the network.
//
// Each Connect stream owns exactly one PeerGroup: the first message must be a
// NewPeerGroupRequest, after which streams are multiplexed over the connection
// by stream_id. Closing the connection closes the group and all its streams.
type PeerGroupServer struct {
	creproxy.UnimplementedPeerGroupProxyServer

	pgFactory creproxy.PeerGroupFactory
}

// NewPeerGroupServer returns a PeerGroupServer serving groups created by the
// given factory.
func NewPeerGroupServer(pgFactory creproxy.PeerGroupFactory) *PeerGroupServer {
	return &PeerGroupServer{pgFactory: pgFactory}
}

func (s *PeerGroupServer) Connect(stream creproxy.PeerGroupProxy_ConnectServer) error {
	req, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("failed to receive initial NewPeerGroupRequest: %w", err)
	}

	newGroupReq, ok := req.Message.(*creproxy.PeerGroupClientRequest_NewPeerGroup)
	if !ok {
		return fmt.Errorf("first message must be NewPeerGroupRequest, got %T", req.Message)
	}

	group, err := s.newPeerGroup(newGroupReq.NewPeerGroup)
	if err != nil {
		return fmt.Errorf("failed to create peer group: %w", err)
	}

	c := &peerGroupConn{stream: stream, group: group, streams: map[string]creproxy.PeerGroupStream{}}
	defer c.close()

	for {
		req, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}

		switch msg := req.Message.(type) {
		case *creproxy.PeerGroupClientRequest_NewPeerGroup:
			return fmt.Errorf("NewPeerGroupRequest not allowed after initial setup")
		case *creproxy.PeerGroupClientRequest_NewStream:
			if err := c.newStream(msg.NewStream); err != nil {
				return fmt.Errorf("failed to create stream: %w", err)
			}
		case *creproxy.PeerGroupClientRequest_StreamSend:
			c.sendTo(msg.StreamSend.StreamId, msg.StreamSend.Payload)
		case *creproxy.PeerGroupClientRequest_CloseStream:
			c.closeStream(msg.CloseStream.StreamId)
		}
	}
}

func (s *PeerGroupServer) newPeerGroup(req *creproxy.NewPeerGroupRequest) (creproxy.PeerGroup, error) {
	if len(req.ConfigDigest) != configDigestLen {
		return nil, fmt.Errorf("invalid config digest length: got %d, expected %d", len(req.ConfigDigest), configDigestLen)
	}
	var configDigest [configDigestLen]byte
	copy(configDigest[:], req.ConfigDigest)

	bootstrappers := make([]creproxy.BootstrapperInfo, len(req.V2Bootstrappers))
	for i, b := range req.V2Bootstrappers {
		bootstrappers[i] = creproxy.BootstrapperInfo{
			PeerID: b.PeerId,
			Addrs:  b.Addrs,
		}
	}

	return s.pgFactory.NewPeerGroup(configDigest, req.PeerIds, bootstrappers)
}

// peerGroupConn holds the per-connection state for a single PeerGroup.
type peerGroupConn struct {
	stream creproxy.PeerGroupProxy_ConnectServer
	group  creproxy.PeerGroup

	// sendMu serializes sends on the gRPC stream, which is written to by one
	// goroutine per stream's receive loop.
	sendMu sync.Mutex

	mu      sync.Mutex
	streams map[string]creproxy.PeerGroupStream
	wg      sync.WaitGroup
}

func (c *peerGroupConn) send(m *creproxy.PeerGroupServerMessage) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.stream.Send(m)
}

func (c *peerGroupConn) newStream(req *creproxy.NewStreamRequest) error {
	st, err := c.group.NewStream(req.RemotePeerId, creproxy.StreamArgs{
		StreamName:         req.StreamName,
		OutgoingBufferSize: int(req.OutgoingBufferSize),
		IncomingBufferSize: int(req.IncomingBufferSize),
		MaxMessageLength:   int(req.MaxMessageLength),
		MessagesLimit:      rateLimit(req.MessagesLimit),
		BytesLimit:         rateLimit(req.BytesLimit),
	})
	if err != nil {
		return err
	}

	c.mu.Lock()
	c.streams[req.StreamId] = st
	c.mu.Unlock()

	streamID := req.StreamId
	recv := st.ReceiveMessages()
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for payload := range recv {
			if err := c.send(&creproxy.PeerGroupServerMessage{
				Message: &creproxy.PeerGroupServerMessage_StreamRecv{
					StreamRecv: &creproxy.StreamRecv{StreamId: streamID, Payload: payload},
				},
			}); err != nil {
				return
			}
		}
	}()
	return nil
}

func (c *peerGroupConn) sendTo(streamID string, payload []byte) {
	c.mu.Lock()
	st := c.streams[streamID]
	c.mu.Unlock()
	if st != nil {
		st.SendMessage(payload)
	}
}

func (c *peerGroupConn) closeStream(streamID string) {
	c.mu.Lock()
	st := c.streams[streamID]
	delete(c.streams, streamID)
	c.mu.Unlock()
	if st != nil {
		_ = st.Close()
	}
}

func (c *peerGroupConn) close() {
	c.mu.Lock()
	for id, st := range c.streams {
		_ = st.Close()
		delete(c.streams, id)
	}
	c.mu.Unlock()
	_ = c.group.Close()
	c.wg.Wait()
}

func rateLimit(p *creproxy.TokenBucketParams) creproxy.RateLimit {
	if p == nil {
		return creproxy.RateLimit{}
	}
	return creproxy.RateLimit{Rate: p.Rate, Capacity: p.Capacity}
}
