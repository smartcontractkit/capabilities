// Package wire is the shape of what a node and a gateway say to each other.
//
// It is one place so that the two sides cannot drift: the paths, the header
// names, and the envelopes are declared here and used by both the connector
// (node side) and the server (gateway side).
//
// The transport is ordinary HTTP over a kept-alive connection rather than a
// websocket. Three things have to happen over it, and each is a request:
//
//   - a node proves who it is (Connect, then Finish)
//   - a node asks the gateway to do something and waits for the answer (Request)
//   - a gateway asks a node to do something: the node holds a request open
//     (Receive) until there is one, answers it out of band (Send), and asks again
//
// The last of those is what a websocket was carrying before. Long-polling is
// what replaces it: the node always has one Receive in flight, so the gateway
// hands a trigger straight to a waiting request rather than queueing it.
package wire

import (
	"encoding/json"
	"time"
)

// Paths a gateway serves for the nodes of its DONs. They are grouped under /node
// so that what a node may reach is separable from what a user may reach - by a
// listener, by a firewall, or by a reverse proxy in front of both.
const (
	PathConnect = "/node/connect"
	PathFinish  = "/node/connect/finish"
	PathReceive = "/node/receive"
	PathSend    = "/node/send"
	PathRequest = "/node/request"

	// PathUser is where a customer's JSON-RPC request arrives. It is the path the
	// gateway serves today, unchanged: what changed is behind it, not in front.
	PathUser = "/"
)

// Header names. Authorization carries the signed handshake header on Connect and
// the session token afterwards, which is what an HTTP client already knows how to
// keep on every request.
const (
	HeaderAuthorization = "Authorization"

	// Scheme prefixes the Authorization value, as HTTP asks: "CRE <value>".
	Scheme = "CRE"
)

// ConnectReply is a gateway's answer to a signed header: something to sign that
// the node could not have known in advance.
type ConnectReply struct {
	// AttemptID names this handshake, so the answer can be matched to the challenge
	// without the gateway having to guess which node is finishing which attempt.
	AttemptID string `json:"attemptId"`

	// Challenge is the packed challenge, base64 in JSON.
	Challenge []byte `json:"challenge"`
}

// FinishRequest is the node's answer to a challenge.
type FinishRequest struct {
	AttemptID string `json:"attemptId"`

	// Signature is over the challenge exactly as it was received.
	Signature []byte `json:"signature"`
}

// FinishReply is what a node uses for the rest of the connection's life.
//
// The token is not a credential in the ordinary sense: it is only accepted on the
// connection it was issued on, so it cannot be lifted from a log and used
// elsewhere. A node that reconnects handshakes again, which costs one signature.
type FinishReply struct {
	Token string `json:"token"`

	// ExpiresIn is how long the token is good for, in seconds, so a node can renew
	// before the gateway forgets it rather than after.
	ExpiresIn int64 `json:"expiresIn"`
}

// Envelope carries a JSON-RPC message in either direction.
//
// The message is passed through as it was signed. Everything a node sends is
// signed by the node, and everything the gateway hands a node was signed by
// whoever sent it - so this adds no trust of its own, and takes none away.
type Envelope struct {
	// GatewayID is which gateway the message is from or for. A node may be connected
	// to several, and a response has to go back to the one that asked.
	GatewayID string `json:"gatewayId"`

	// Message is the raw JSON-RPC request or response.
	Message json.RawMessage `json:"message"`
}

// DefaultReceiveTimeout is how long a gateway holds an unanswered Receive before
// replying that there is nothing yet.
//
// It is a compromise between idle requests and reconnection churn: shorter means
// more requests that carry nothing, longer means an idle connection that
// something in the middle - a load balancer, a NAT - may decide to drop.
const DefaultReceiveTimeout = 30 * time.Second

// DefaultSessionTTL is how long a session token is honoured, absent renewal.
const DefaultSessionTTL = time.Hour
