package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	"github.com/smartcontractkit/capabilities/http/gateway/jwt"
)

// triggers waits for the DON to agree, which is the part that cannot be skipped:
// each node answers for itself, and one node's answer is one node's word. F+1
// saying the same thing is why a compromised node cannot make up an execution ID.
type triggers struct {
	lggr     logger.Logger
	metadata *metadata
	send     func(node string, req *jsonrpc.Request[json.RawMessage]) error
	nodes    func() []string

	agreement int

	timeout time.Duration

	// So a token authorises one request rather than as many as anyone who saw it
	// cares to make.
	replay *replayCache

	mu      sync.Mutex
	pending map[string]*exchange
}

type exchange struct {
	// answers is digest -> how many nodes gave it, and which nodes have answered at
	// all, so that a node cannot vote twice.
	answers map[string]int
	voted   map[string]bool

	// done carries the agreed answer to whoever is waiting, once.
	done chan *jsonrpc.Response[json.RawMessage]
	once sync.Once
}

func newTriggers(
	lggr logger.Logger,
	metadata *metadata,
	send func(node string, req *jsonrpc.Request[json.RawMessage]) error,
	nodes func() []string,
	agreement int,
	timeout time.Duration,
	replayFor time.Duration,
) *triggers {
	if agreement < 1 {
		agreement = 1
	}
	return &triggers{
		lggr:      lggr,
		metadata:  metadata,
		send:      send,
		nodes:     nodes,
		agreement: agreement,
		timeout:   timeout,
		replay:    newReplayCache(replayFor),
		pending:   map[string]*exchange{},
	}
}

// Handle takes a customer's JSON-RPC request and answers it.
//
// The order of what it refuses matters as much as what it accepts: a request is
// checked for shape, then for who signed it, then for whether that signer may run
// this workflow - so a caller learns nothing about a workflow they cannot run
// beyond that they cannot run it.
func (t *triggers) Handle(ctx context.Context, req *jsonrpc.Request[json.RawMessage]) *jsonrpc.Response[json.RawMessage] {
	if req.ID == "" {
		return userError(req, jsonrpc.ErrInvalidRequest, "request ID is required")
	}
	if req.Method != gateway.MethodWorkflowExecute {
		return userError(req, jsonrpc.ErrMethodNotFound, fmt.Sprintf("unsupported method %q", req.Method))
	}
	if req.Params == nil {
		return userError(req, jsonrpc.ErrInvalidParams, "params are required")
	}

	var trigger gateway.HTTPTriggerRequest
	if err := json.Unmarshal(*req.Params, &trigger); err != nil {
		return userError(req, jsonrpc.ErrInvalidParams, "params are not an HTTP trigger request: "+err.Error())
	}

	workflowID, known := t.metadata.Resolve(trigger.Workflow)
	if !known {
		return userError(req, jsonrpc.ErrInvalidParams, "no workflow matches that selector")
	}

	claims, signer, err := jwt.Verify(req.Auth, *req)
	if err != nil {
		return userError(req, jsonrpc.ErrInvalidRequest, "Auth failure: "+err.Error())
	}
	if !t.replay.take(claims.ID) {
		return userError(req, jsonrpc.ErrInvalidRequest,
			"Auth failure: this token has already been used. Please generate a new one with a new id (jti)")
	}
	if !t.metadata.Authorized(workflowID, signer) {
		return userError(req, jsonrpc.ErrInvalidRequest, fmt.Sprintf(
			"Auth failure: signer '%s' is not authorized for workflow '%s'. Ensure that the signer is registered in the workflow definition",
			signer, workflowID))
	}

	return t.execute(ctx, req, workflowID)
}

// execute sends the request to every node of the DON and waits for them to agree.
func (t *triggers) execute(ctx context.Context, req *jsonrpc.Request[json.RawMessage], workflowID string) *jsonrpc.Response[json.RawMessage] {
	nodes := t.nodes()
	if len(nodes) < t.agreement {
		return userError(req, jsonrpc.ErrInternal, fmt.Sprintf(
			"the DON is not reachable: %d of the %d nodes needed to agree are connected", len(nodes), t.agreement))
	}

	waiting := &exchange{
		answers: map[string]int{},
		voted:   map[string]bool{},
		done:    make(chan *jsonrpc.Response[json.RawMessage], 1),
	}

	t.mu.Lock()
	if _, inFlight := t.pending[req.ID]; inFlight {
		t.mu.Unlock()
		return userError(req, jsonrpc.ErrInvalidRequest, "a request with this ID is already in flight")
	}
	t.pending[req.ID] = waiting
	t.mu.Unlock()

	defer func() {
		t.mu.Lock()
		delete(t.pending, req.ID)
		t.mu.Unlock()
	}()

	var sent int
	for _, node := range nodes {
		if err := t.send(node, req); err != nil {
			// One node that cannot be reached is not a failed request: the DON only needs
			// F+1 of them, which is the point of there being several.
			t.lggr.Warnw("Failed to send a trigger to a node", "node", node, "requestID", req.ID, "err", err)
			continue
		}
		sent++
	}
	if sent < t.agreement {
		return userError(req, jsonrpc.ErrInternal, fmt.Sprintf(
			"the DON is not reachable: the request reached %d of the %d nodes needed to agree", sent, t.agreement))
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	select {
	case response := <-waiting.done:
		return response
	case <-ctx.Done():
		return userError(req, jsonrpc.ErrInternal, "the DON did not agree on an answer in time")
	}
}

// Answer takes one node's answer to a request in flight.
func (t *triggers) Answer(node string, resp *jsonrpc.Response[json.RawMessage]) error {
	t.mu.Lock()
	waiting, inFlight := t.pending[resp.ID]
	t.mu.Unlock()
	if !inFlight {
		// Late, or for a request this gateway never made. Not an error: a node answering
		// after the customer has been answered is the ordinary end of a race.
		return nil
	}

	digest, err := digestOf(resp)
	if err != nil {
		return err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if waiting.voted[node] {
		return fmt.Errorf("node %s answered request %s twice", node, resp.ID)
	}
	waiting.voted[node] = true
	waiting.answers[digest]++

	if waiting.answers[digest] >= t.agreement {
		waiting.once.Do(func() { waiting.done <- resp })
	}
	return nil
}

// digestOf is how two nodes' answers are compared: the same bytes are the same
// answer. The ID is left out because it is the same by construction, and the
// result is compared as it was serialised so that nothing about ordering can make
// two identical answers look different.
func digestOf(resp *jsonrpc.Response[json.RawMessage]) (string, error) {
	switch {
	case resp.Error != nil:
		encoded, err := json.Marshal(resp.Error)
		if err != nil {
			return "", err
		}
		return "error:" + string(encoded), nil
	case resp.Result != nil:
		return "result:" + string(*resp.Result), nil
	default:
		return "", errors.New("a response with neither a result nor an error")
	}
}

func userError(req *jsonrpc.Request[json.RawMessage], code int64, message string) *jsonrpc.Response[json.RawMessage] {
	return &jsonrpc.Response[json.RawMessage]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      req.ID,
		Error:   &jsonrpc.WireError{Code: code, Message: message},
	}
}

// replayCache remembers the tokens that have been spent.
//
// A JWT authorises one request. Without this, anyone who saw a token could repeat
// the request it authorised until the token expired - which for a workflow that
// moves money is not a small thing.
type replayCache struct {
	ttl time.Duration

	mu   sync.Mutex
	seen map[string]time.Time
}

func newReplayCache(ttl time.Duration) *replayCache {
	return &replayCache{ttl: ttl, seen: map[string]time.Time{}}
}

// take records a token ID and reports whether it was unused.
func (c *replayCache) take(id string) bool {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if used, ok := c.seen[id]; ok && now.Sub(used) < c.ttl {
		return false
	}
	c.seen[id] = now
	return true
}

// expire forgets tokens that could no longer be used anyway.
func (c *replayCache) expire() {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	for id, used := range c.seen {
		if now.Sub(used) >= c.ttl {
			delete(c.seen, id)
		}
	}
}
