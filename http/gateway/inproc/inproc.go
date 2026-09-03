// Package inproc is the gateway and its nodes in one process, talking by
// function call.
//
// It is for an embedded run, where the "DON" is a handful of goroutines and the
// gateway is one more. Dialling itself over HTTP would prove nothing: a loopback
// socket between two objects in the same heap is a moving part with no purpose.
//
// Everything above the connection is the same code as a deployed run: the same
// gateway, the same agreement threshold, the same capabilities. What is missing
// is what only a network needs - the handshake, the sessions, the polling - and
// with it the identity those establish. An embedded run has no identity to
// establish: its keys are derived from instance indices and are public by
// construction.
//
// It is also the seam a direct caller can use. Nothing in "run" or "embed" builds
// one of these by hand, but the gateway's constructor takes the connections it
// serves as an interface, so a caller assembling its own - a test, a tool, a
// harness - can substitute this for the HTTP transport.
package inproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

// Gateway is what a node needs of the gateway: somewhere to send what it has to
// say. It is an interface so that this package does not have to know what else a
// gateway is.
type Gateway interface {
	HandleNodeMessage(ctx context.Context, donID, node string, msg *jsonrpc.Response[json.RawMessage]) error

	// And somewhere to ask, for what a node waits on rather than hears about later.
	AnswerNodeMessage(ctx context.Context, donID, node string, msg *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error)
}

// Nodes satisfies the same interface the HTTP transport does, so the gateway
// cannot tell which it has.
type Nodes struct {
	lggr logger.Logger

	mu    sync.RWMutex
	nodes map[string]*Connector
}

func NewNodes(lggr logger.Logger) *Nodes {
	return &Nodes{lggr: lggr, nodes: map[string]*Connector{}}
}

// On its own goroutine, because the caller may be that node: a gateway answering
// an outbound request sends to the node that asked, and would otherwise be
// waiting on the call it is inside.
func (n *Nodes) Send(node string, req *jsonrpc.Request[json.RawMessage]) error {
	n.mu.RLock()
	connector, known := n.nodes[node]
	n.mu.RUnlock()
	if !known {
		return fmt.Errorf("no node %s in this process", node)
	}

	go connector.deliver(req)
	return nil
}

// There is no connection to lose, so every node is reachable for as long as the
// process is.
func (n *Nodes) Connected(string) []string {
	n.mu.RLock()
	defer n.mu.RUnlock()

	return slices.Sorted(maps.Keys(n.nodes))
}

// Connector returns what the capabilities in that instance are handed.
func (n *Nodes) Connector(gateway Gateway, gatewayID, donID, node string) *Connector {
	c := &Connector{
		lggr:      logger.Named(n.lggr, "Node."+node),
		gateway:   gateway,
		gatewayID: gatewayID,
		donID:     donID,
		node:      node,
		handlers:  map[string]core.GatewayConnectorHandler{},
	}

	n.mu.Lock()
	n.nodes[node] = c
	n.mu.Unlock()

	return c
}

type Connector struct {
	lggr      logger.Logger
	gateway   Gateway
	gatewayID string
	donID     string
	node      string

	mu       sync.RWMutex
	handlers map[string]core.GatewayConnectorHandler
}

var _ core.MultiGatewayConnector = (*Connector)(nil)

func (c *Connector) deliver(req *jsonrpc.Request[json.RawMessage]) {
	c.mu.RLock()
	handler, ok := c.handlers[req.Method]
	c.mu.RUnlock()
	if !ok {
		c.lggr.Warnw("No handler for a method the gateway sent", "method", req.Method)
		return
	}

	if err := handler.HandleGatewayMessage(context.Background(), c.gatewayID, req); err != nil {
		c.lggr.Errorw("Handler rejected a gateway message", "method", req.Method, "err", err)
	}
}

func (c *Connector) AddHandler(_ context.Context, methods []string, handler core.GatewayConnectorHandler) error {
	if handler == nil {
		return errors.New("a handler is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, method := range methods {
		if _, taken := c.handlers[method]; taken {
			return fmt.Errorf("method %q already has a handler", method)
		}
	}
	for _, method := range methods {
		c.handlers[method] = handler
	}
	return nil
}

func (c *Connector) RemoveHandler(_ context.Context, methods []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, method := range methods {
		delete(c.handlers, method)
	}
	return nil
}

// The node this is attributed to is known rather than proved: in one process
// there is nobody else it could be.
func (c *Connector) SendToGateway(ctx context.Context, _ string, resp *jsonrpc.Response[json.RawMessage]) error {
	return c.gateway.HandleNodeMessage(ctx, c.donID, c.node, resp)
}

// Request is the same call with the answer returned rather than delivered, which
// in one process is what it always was: over the network it is a request the node
// holds open, and here it is the call the caller is already inside.
func (c *Connector) Request(ctx context.Context, _ string, resp *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error) {
	return c.gateway.AnswerNodeMessage(ctx, c.donID, c.node, resp)
}

// SignMessage refuses: a signature is how a message is attributed across a
// network, and there is no network here.
func (c *Connector) SignMessage(context.Context, []byte) ([]byte, error) {
	return nil, errors.New("an in-process gateway connection does not sign: it knows which node it is")
}

func (c *Connector) GatewayIDs(context.Context) ([]string, error) {
	return []string{c.gatewayID}, nil
}

func (c *Connector) GatewayIDsForDon(context.Context, string) ([]string, error) {
	return []string{c.gatewayID}, nil
}

func (c *Connector) DonID(context.Context) (string, error) { return c.donID, nil }

func (c *Connector) DonIDForGateway(context.Context, string) (string, error) { return c.donID, nil }

func (c *Connector) PrimaryDonID(context.Context) (string, error) { return c.donID, nil }

// At once: the gateway is in this process, and either it exists or this could not
// have been built.
func (c *Connector) AwaitConnection(context.Context, string) error { return nil }
