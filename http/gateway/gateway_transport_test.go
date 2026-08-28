// Package gateway_test covers the two halves of the node connection together:
// the connector a capability is handed, and the gateway that answers it.
//
// They are tested as a pair because what matters is the property neither half
// holds alone - that a gateway only ever talks to a node whose key signed for the
// connection, and that a message reaches the other side unchanged.
package main_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	"github.com/smartcontractkit/capabilities/http/gateway/connector"
	"github.com/smartcontractkit/capabilities/http/gateway/server"
	"github.com/smartcontractkit/capabilities/http/gateway/service"
)

const (
	gatewayID = "gateway_1"
	donID     = "workflow_don"
)

// node is a key and the address it signs as, which is all a node is to a gateway.
type node struct {
	key     *secp256k1.PrivateKey
	address string
}

func newNode(t *testing.T) node {
	t.Helper()

	key, err := secp256k1.GeneratePrivateKey()
	require.NoError(t, err)

	address, err := auth.AddressOf(key.PubKey().SerializeUncompressed())
	require.NoError(t, err)
	return node{key: key, address: address}
}

// Sign signs the way the node's chain key does: 65 bytes, recovery byte last.
func (n node) Sign(_ context.Context, hash []byte) ([]byte, error) {
	compact := dcrecdsa.SignCompact(n.key, hash, false)

	// dcrd puts the recovery byte first and offsets it by 27; the form everything
	// here uses puts it last, as 0 or 1.
	signature := make([]byte, 0, auth.SignatureLen)
	signature = append(signature, compact[1:]...)
	return append(signature, compact[0]-27), nil
}

// collector is a gateway that remembers what nodes said to it.
type collector struct {
	mu       sync.Mutex
	messages []string
	nodes    []string
}

func (c *collector) HandleNodeMessage(_ context.Context, _, node string, msg *jsonrpc.Response[json.RawMessage]) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = append(c.messages, string(*msg.Result))
	c.nodes = append(c.nodes, node)
	return nil
}

func (c *collector) seen() ([]string, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.messages...), append([]string(nil), c.nodes...)
}

// handler is a capability's end: it records what the gateway sent it.
type handler struct {
	received chan *jsonrpc.Request[json.RawMessage]
}

func (h *handler) ID(context.Context) (string, error) { return "test", nil }

func (h *handler) HandleGatewayMessage(_ context.Context, _ string, req *jsonrpc.Request[json.RawMessage]) error {
	h.received <- req
	return nil
}

// serve starts a gateway serving the nodes it is given, and returns its URL and
// the transport behind it.
func serve(t *testing.T, gateway *collector, nodes ...node) (string, *server.Transport) {
	t.Helper()

	addresses := make([]string, 0, len(nodes))
	for _, n := range nodes {
		addresses = append(addresses, n.address)
	}

	transport, err := server.NewTransport(logger.Test(t), server.Config{
		GatewayID: gatewayID,
		// Short, so a poll that finds nothing comes back inside a test's patience.
		ReceiveTimeout: 200 * time.Millisecond,
	}, auth.DONs{donID: addresses}, gateway)
	require.NoError(t, err)

	mux := http.NewServeMux()
	transport.Routes(mux)

	// The proxy shares this listener, as it does in the gateway binary: control
	// traffic is HTTP/2 and a tunnel is an HTTP/1.1 CONNECT, so one address serves
	// both and a node has one address to be told about.
	tunnel, err := service.NewTunnel(logger.Test(t), service.TunnelConfig{GatewayID: gatewayID}, auth.DONs{donID: addresses})
	require.NoError(t, err)

	// The session is pinned to the connection it was issued on, so the server has to
	// be the one that records connections - which is what ConnContext is for.
	srv := httptest.NewUnstartedServer(server.Serve(mux, tunnel))
	srv.Config.ConnContext = server.ConnContext
	srv.Start()
	t.Cleanup(srv.Close)

	return srv.URL, transport
}

func connect(t *testing.T, url string, n node) *connector.Connector {
	t.Helper()

	c, err := connector.New(logger.Test(t), connector.Config{
		NodeAddress:    n.address,
		DonID:          donID,
		Gateways:       []string{gatewayID + "=" + url},
		ReceiveTimeout: 200 * time.Millisecond,
		RetryInterval:  50 * time.Millisecond,
	}, auth.SignerFunc(n.Sign))
	require.NoError(t, err)

	servicetest.Run(t, c)
	return c
}

// TestConnection is the round trip: a node proves who it is, the gateway sends it
// a request, and the answer comes back attributed to that node.
func TestConnection(t *testing.T) {
	n := newNode(t)
	gateway := &collector{}
	url, transport := serve(t, gateway, n)

	c := connect(t, url, n)

	h := &handler{received: make(chan *jsonrpc.Request[json.RawMessage], 1)}
	require.NoError(t, c.AddHandler(t.Context(), []string{"do_something"}, h))

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	require.NoError(t, c.AwaitConnection(ctx, gatewayID))

	t.Run("the gateway knows which nodes are connected", func(t *testing.T) {
		assert.Equal(t, []string{n.address}, transport.Connected(donID))
	})

	t.Run("a request reaches the node", func(t *testing.T) {
		require.NoError(t, transport.Send(n.address, &jsonrpc.Request[json.RawMessage]{
			Version: "2.0", ID: "1", Method: "do_something",
		}))

		select {
		case req := <-h.received:
			assert.Equal(t, "do_something", req.Method)
		case <-time.After(10 * time.Second):
			t.Fatal("the node never received the request")
		}
	})

	t.Run("the answer comes back, attributed to the node that signed for the connection", func(t *testing.T) {
		result := json.RawMessage(`{"answered":true}`)
		require.NoError(t, c.SendToGateway(t.Context(), gatewayID, &jsonrpc.Response[json.RawMessage]{
			Version: "2.0", ID: "1", Result: &result,
		}))

		assert.Eventually(t, func() bool {
			messages, _ := gateway.seen()
			return len(messages) == 1
		}, 10*time.Second, 20*time.Millisecond)

		messages, nodes := gateway.seen()
		assert.Equal(t, `{"answered":true}`, messages[0])
		assert.Equal(t, n.address, nodes[0], "the gateway must attribute the message to the node that authenticated")
	})
}

// TestUnknownNodeIsRefused is the membership check: a well-formed handshake from
// a key the DON does not list gets nowhere.
func TestUnknownNodeIsRefused(t *testing.T) {
	member, stranger := newNode(t), newNode(t)
	url, _ := serve(t, &collector{}, member)

	c, err := connector.New(logger.Test(t), connector.Config{
		NodeAddress:    stranger.address,
		DonID:          donID,
		Gateways:       []string{gatewayID + "=" + url},
		ReceiveTimeout: 200 * time.Millisecond,
		RetryInterval:  50 * time.Millisecond,
	}, auth.SignerFunc(stranger.Sign))
	require.NoError(t, err)

	servicetest.Run(t, c)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.Error(t, c.AwaitConnection(ctx, gatewayID), "a node the DON does not list must not connect")
}

// TestSessionIsPinnedToItsConnection is what replaces the websocket's guarantee:
// the token says nothing on a connection other than the one that proved who it
// was, so a token that leaked is a token that cannot be used.
func TestSessionIsPinnedToItsConnection(t *testing.T) {
	n := newNode(t)
	url, _ := serve(t, &collector{}, n)

	token := handshake(t, url, n)

	// A transport of its own, so this really is a second connection: clients that
	// share one - which is what the default transport does - share its connections
	// too, and would be indistinguishable from the client that handshaked.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+"/node/receive", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "CRE "+token)

	resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, resp.Body.Close()) })

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "a session must not travel between connections")
}

// handshake performs the two round trips by hand and returns the session token,
// so that a test can then try to use it from somewhere else.
func handshake(t *testing.T, url string, n node) string {
	t.Helper()

	client := &http.Client{Transport: &http.Transport{}}

	header, err := auth.PackHeader(auth.Header{
		Timestamp: uint32(time.Now().Unix()), //#nosec G115 - test
		DonID:     donID,
		GatewayID: gatewayID,
	})
	require.NoError(t, err)

	signature, err := n.Sign(t.Context(), auth.Hash(header))
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/node/connect", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "CRE "+base64.StdEncoding.EncodeToString(append(header, signature...)))

	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() { assert.NoError(t, resp.Body.Close()) }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var challenge struct {
		AttemptID string `json:"attemptId"`
		Challenge []byte `json:"challenge"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&challenge))

	answer, err := n.Sign(t.Context(), auth.Hash(challenge.Challenge))
	require.NoError(t, err)

	body, err := json.Marshal(map[string]any{"attemptId": challenge.AttemptID, "signature": answer})
	require.NoError(t, err)

	finish, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url+"/node/connect/finish", strings.NewReader(string(body)))
	require.NoError(t, err)

	finished, err := client.Do(finish)
	require.NoError(t, err)
	defer func() { assert.NoError(t, finished.Body.Close()) }()
	require.Equal(t, http.StatusOK, finished.StatusCode)

	var session struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.NewDecoder(finished.Body).Decode(&session))
	require.NotEmpty(t, session.Token)
	return session.Token
}

var _ core.GatewayConnectorHandler = (*handler)(nil)
