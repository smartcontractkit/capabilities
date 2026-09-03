// Package wire is the shape of what a node and a gateway say to each other.
//
// It is one place so that the two sides cannot drift: the connector (node side)
// and the server (gateway side) both read it from here.
//
// The transport is ordinary HTTP over a kept-alive connection rather than a
// websocket. Long-polling is what replaces the socket: the node always has one
// Receive in flight, so the gateway hands a trigger straight to a waiting request
// rather than queueing it.
package wire

import (
	"encoding/json"
	"time"
)

// Grouped under /node so that what a node may reach is separable from what a user
// may reach - by a listener, by a firewall, or by a reverse proxy in front of both.
const (
	PathConnect = "/node/connect"
	PathFinish  = "/node/connect/finish"
	PathReceive = "/node/receive"
	PathSend    = "/node/send"

	// PathRequest is for what a node cannot get on with without: it asks, and the
	// answer comes back on the request it asked with rather than on its next poll.
	//
	// Send and receive are the other shape - a node says something and hears about
	// the consequence later - and they stay that shape because they are what a
	// gateway pushing triggers at a node needs. An outbound HTTP request is not that:
	// the workflow that made it is waiting, so the exchange is one request, and
	// nothing has to match an answer to a question it has forgotten.
	PathRequest = "/node/request"

	// The path the gateway serves today, unchanged: what changed is behind it.
	PathUser = "/"
)

const (
	HeaderAuthorization = "Authorization"
	Scheme              = "CRE"
)

type ConnectReply struct {
	// So the answer can be matched to the challenge without the gateway having to
	// guess which node is finishing which attempt.
	AttemptID string `json:"attemptId"`
	Challenge []byte `json:"challenge"`
}

type FinishRequest struct {
	AttemptID string `json:"attemptId"`
	// Over the challenge exactly as it was received.
	Signature []byte `json:"signature"`
}

// FinishReply is what a node uses for the rest of the connection's life.
//
// The token is not a credential in the ordinary sense: it is only accepted on the
// connection it was issued on, so it cannot be lifted from a log and used
// elsewhere. A node that reconnects handshakes again, which costs one signature.
type FinishReply struct {
	Token string `json:"token"`
	// Seconds, so a node can renew before the gateway forgets it rather than after.
	ExpiresIn int64 `json:"expiresIn"`
}

// Envelope carries a JSON-RPC message in either direction.
//
// The message is passed through as it was signed: everything a node sends is
// signed by the node, and everything the gateway hands a node was signed by
// whoever sent it, so this adds no trust of its own and takes none away.
type Envelope struct {
	// A node may be connected to several gateways, and a response has to go back to
	// the one that asked.
	GatewayID string          `json:"gatewayId"`
	Message   json.RawMessage `json:"message"`
}

// DefaultReceiveTimeout is a compromise between idle requests and reconnection
// churn: shorter means more requests that carry nothing, longer means an idle
// connection that something in the middle - a load balancer, a NAT - may drop.
const DefaultReceiveTimeout = 30 * time.Second

const DefaultSessionTTL = time.Hour
