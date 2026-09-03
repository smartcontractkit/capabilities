// Package server is the gateway's end of the node connection.
//
// The identity checks are the ones the websocket handshake made: a signed header,
// a challenge, a signature over it. What differs is that a websocket held the
// identity for as long as the socket lived and HTTP has to say so per request. A
// session token does that, and is only honoured on the connection it was issued
// on - so a token read from a log or a proxy's memory buys nothing, and the
// property the socket gave (this is the same peer that authenticated) is kept.
package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	"github.com/smartcontractkit/capabilities/http/gateway/wire"
)

// Handler is what the gateway does with what a node says.
//
// This file knows who sent a message and nothing else; what one means - a
// trigger's answer, a fetch to perform - belongs to the handlers.
type Handler interface {
	// The node is the authenticated one, and the message is passed through as it was
	// signed.
	HandleNodeMessage(ctx context.Context, donID, node string, msg *jsonrpc.Response[json.RawMessage]) error

	// AnswerNodeMessage is the same, for a node that is waiting: what it returns
	// goes back on the request the node sent, rather than to its mailbox.
	AnswerNodeMessage(ctx context.Context, donID, node string, msg *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error)
}

type Transport struct {
	lggr      logger.Logger
	gatewayID string
	verifier  auth.Verifier
	handler   Handler

	tolerance time.Duration
	ttl       time.Duration

	receiveTimeout time.Duration

	mailboxSize int

	mu       sync.Mutex
	attempts map[string]*attempt
	sessions map[string]*session
	mailbox  map[string]*mailbox
}

// attempt is a challenge issued to a node that has proved nothing yet.
type attempt struct {
	donID     string
	node      string
	challenge []byte
	conn      string
	issued    time.Time
}

type session struct {
	donID   string
	node    string
	conn    string
	expires time.Time
}

// One channel per node rather than a queue per connection: a node with no poll in
// flight for a moment - between one returning and the next going out - should not
// lose what arrives in that moment.
type mailbox struct {
	messages chan wire.Envelope
}

type Config struct {
	// A header signed for another gateway is refused, so this has to be the name the
	// nodes were configured with.
	GatewayID string

	TimestampTolerance time.Duration

	SessionTTL time.Duration

	ReceiveTimeout time.Duration

	Mailbox int
}

// The handler may be nil here and set with Handles: the gateway needs the
// transport to be built (it is where its nodes are) and the transport needs the
// gateway (it is what messages are for), so one of them is second.
func NewTransport(lggr logger.Logger, cfg Config, verifier auth.Verifier, handler Handler) (*Transport, error) {
	if cfg.GatewayID == "" {
		return nil, errors.New("a gateway needs an ID: it is what nodes sign their headers for")
	}
	if verifier == nil {
		return nil, errors.New("a gateway needs to know which nodes belong to which DON")
	}
	if cfg.TimestampTolerance <= 0 {
		cfg.TimestampTolerance = auth.DefaultTimestampTolerance
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = wire.DefaultSessionTTL
	}
	if cfg.ReceiveTimeout <= 0 {
		cfg.ReceiveTimeout = wire.DefaultReceiveTimeout
	}
	if cfg.Mailbox <= 0 {
		cfg.Mailbox = 256
	}

	return &Transport{
		lggr:           lggr,
		gatewayID:      cfg.GatewayID,
		verifier:       verifier,
		handler:        handler,
		tolerance:      cfg.TimestampTolerance,
		ttl:            cfg.SessionTTL,
		receiveTimeout: cfg.ReceiveTimeout,
		mailboxSize:    cfg.Mailbox,
		attempts:       map[string]*attempt{},
		sessions:       map[string]*session{},
		mailbox:        map[string]*mailbox{},
	}, nil
}

// MaxConcurrentStreams is how many requests a node may have in flight on its one
// connection.
//
// It has to be generous, because a node's requests are held open for as long as
// the far side takes: a fetch the gateway is making occupies a stream until it
// answers, and so does the long poll. The cost of the limit being reached is not
// an error but a stall - the node's transport queues the next request rather than
// opening a second connection, which its session would not be honoured on - so
// what this really sets is how much of a DON's traffic one gateway is willing to
// have outstanding at once.
const MaxConcurrentStreams = 4096

// Serve wraps a mux so that a plaintext listener speaks HTTP/2, which it has to:
// a session is pinned to one connection, and a node's long-poll would otherwise
// hold a whole HTTP/1.1 connection while the answers to it went out on another.
// A gateway serving TLS negotiates HTTP/2 anyway; wrapping is harmless there.
//
// The CONNECTs go to connect, so the proxy shares this listener. They never
// collide - a tunnel takes over its connection, leaving nothing to multiplex -
// and a node that can reach one can reach the other, so two addresses to
// configure would be two things to get wrong.
func Serve(mux http.Handler, connect http.Handler) http.Handler {
	served := h2c.NewHandler(mux, &http2.Server{MaxConcurrentStreams: MaxConcurrentStreams})
	if connect == nil {
		return served
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			connect.ServeHTTP(w, r)
			return
		}
		served.ServeHTTP(w, r)
	})
}

// Handles is for a caller that had to build this first. See NewTransport.
func (t *Transport) Handles(handler Handler) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.handler = handler
}

func (t *Transport) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST "+wire.PathConnect, t.connect)
	mux.HandleFunc("POST "+wire.PathFinish, t.finish)
	mux.HandleFunc("GET "+wire.PathReceive, t.receive)
	mux.HandleFunc("POST "+wire.PathSend, t.send)
	mux.HandleFunc("POST "+wire.PathRequest, t.request)
}

// ConnContext records which connection a request arrived on, which is what a
// session is pinned to. For http.Server.ConnContext.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, connKey{}, c.RemoteAddr().String()+"|"+uuid.NewString())
}

type connKey struct{}

func connOf(r *http.Request) string {
	id, _ := r.Context().Value(connKey{}).(string)
	return id
}

func (t *Transport) connect(w http.ResponseWriter, r *http.Request) {
	header, err := bearer(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	packed, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		http.Error(w, "authorization is not base64", http.StatusBadRequest)
		return
	}

	claimed, node, err := auth.VerifyHeader(t.verifier, t.gatewayID, packed, time.Now(), t.tolerance)
	if err != nil {
		t.lggr.Warnw("Refused a handshake", "err", err, "remote", r.RemoteAddr)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	challenge, err := auth.NewChallenge(t.gatewayID, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	packedChallenge, err := auth.PackChallenge(challenge)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id := uuid.NewString()
	t.mu.Lock()
	t.attempts[id] = &attempt{
		donID:     claimed.DonID,
		node:      node,
		challenge: packedChallenge,
		conn:      connOf(r),
		issued:    time.Now(),
	}
	t.mu.Unlock()

	write(w, wire.ConnectReply{AttemptID: id, Challenge: packedChallenge})
}

func (t *Transport) finish(w http.ResponseWriter, r *http.Request) {
	var body wire.FinishRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "body is not a finish request", http.StatusBadRequest)
		return
	}

	t.mu.Lock()
	pending, ok := t.attempts[body.AttemptID]
	delete(t.attempts, body.AttemptID)
	t.mu.Unlock()
	if !ok {
		http.Error(w, "no such handshake", http.StatusUnauthorized)
		return
	}

	// The same connection the challenge was issued on: a node that started a
	// handshake on one connection and finished it on another is not a shape this
	// serves, and allowing it would be a way to have one peer answer for another.
	if pending.conn != connOf(r) {
		http.Error(w, "handshake finished on a different connection", http.StatusUnauthorized)
		return
	}
	if time.Since(pending.issued) > t.tolerance {
		http.Error(w, "handshake took too long", http.StatusUnauthorized)
		return
	}

	if err := auth.VerifyChallengeResponse(t.verifier, pending.donID, pending.node, pending.challenge, body.Signature); err != nil {
		t.lggr.Warnw("Refused a challenge response", "err", err, "node", pending.node, "remote", r.RemoteAddr)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	token, err := newToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	t.mu.Lock()
	t.sessions[token] = &session{donID: pending.donID, node: pending.node, conn: pending.conn, expires: time.Now().Add(t.ttl)}
	t.mu.Unlock()

	t.lggr.Infow("Node connected", "node", pending.node, "don", pending.donID, "remote", r.RemoteAddr)
	write(w, wire.FinishReply{Token: token, ExpiresIn: int64(t.ttl.Seconds())})
}

func (t *Transport) receive(w http.ResponseWriter, r *http.Request) {
	s, err := t.authenticated(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	box := t.mailboxFor(s.node)
	ctx, cancel := context.WithTimeout(r.Context(), t.receiveTimeout)
	defer cancel()

	select {
	case envelope := <-box.messages:
		write(w, envelope)
	case <-ctx.Done():
		// Which is how most polls end. The node asks again.
		w.WriteHeader(http.StatusNoContent)
	}
}

func (t *Transport) send(w http.ResponseWriter, r *http.Request) {
	s, message, handler, ok := t.from(w, r)
	if !ok {
		return
	}

	if err := handler.HandleNodeMessage(r.Context(), s.donID, s.node, message); err != nil {
		t.lggr.Warnw("Handler rejected a node message", "node", s.node, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// request is send for a node that is waiting: the answer goes back here rather
// than to the node's mailbox, so nothing has to be matched up on either side.
//
// The stream stays open for as long as the answer takes, which is what the
// gateway's HTTP/2 is for: a node's other traffic - its poll, its other requests -
// travels the same connection meanwhile. See MaxConcurrentStreams.
func (t *Transport) request(w http.ResponseWriter, r *http.Request) {
	s, message, handler, ok := t.from(w, r)
	if !ok {
		return
	}

	answer, err := handler.AnswerNodeMessage(r.Context(), s.donID, s.node, message)
	if err != nil {
		t.lggr.Warnw("Handler rejected a node request", "node", s.node, "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	encoded, err := json.Marshal(answer)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	write(w, wire.Envelope{GatewayID: t.gatewayID, Message: encoded})
}

// from is what the two node-to-gateway paths have in common: who sent this, what
// they said, and who here is to deal with it. It answers the request itself when
// any of the three is missing, and reports whether the caller has anything left
// to do.
func (t *Transport) from(w http.ResponseWriter, r *http.Request) (*session, *jsonrpc.Response[json.RawMessage], Handler, bool) {
	s, err := t.authenticated(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return nil, nil, nil, false
	}

	var envelope wire.Envelope
	if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
		http.Error(w, "body is not an envelope", http.StatusBadRequest)
		return nil, nil, nil, false
	}

	var message jsonrpc.Response[json.RawMessage]
	if err := json.Unmarshal(envelope.Message, &message); err != nil {
		http.Error(w, "envelope does not carry a JSON-RPC response", http.StatusBadRequest)
		return nil, nil, nil, false
	}

	t.mu.Lock()
	handler := t.handler
	t.mu.Unlock()
	if handler == nil {
		http.Error(w, "this gateway is not ready to take messages yet", http.StatusServiceUnavailable)
		return nil, nil, nil, false
	}

	return s, &message, handler, true
}

// Send does not wait for the node to take the message: what a caller wants to
// know is whether the node is reachable at all, and a node whose mailbox is full
// is a node that has stopped asking.
func (t *Transport) Send(node string, msg *jsonrpc.Request[json.RawMessage]) error {
	encoded, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to encode the message: %w", err)
	}

	select {
	case t.mailboxFor(node).messages <- wire.Envelope{GatewayID: t.gatewayID, Message: encoded}:
		return nil
	default:
		return fmt.Errorf("node %s is not keeping up: its mailbox is full", node)
	}
}

// Connected is about sessions that have not expired rather than sockets that are
// open: with kept-alive HTTP there is no socket to watch, and a node that stopped
// asking is one whose session lapses.
func (t *Transport) Connected(donID string) []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	var nodes []string
	seen := map[string]bool{}
	for _, s := range t.sessions {
		if s.donID != donID || time.Now().After(s.expires) || seen[s.node] {
			continue
		}
		seen[s.node] = true
		nodes = append(nodes, s.node)
	}
	return nodes
}

func (t *Transport) authenticated(r *http.Request) (*session, error) {
	token, err := bearer(r)
	if err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	s, ok := t.sessions[token]
	if !ok {
		return nil, errors.New("not authenticated: handshake first")
	}
	if time.Now().After(s.expires) {
		delete(t.sessions, token)
		return nil, errors.New("session expired: handshake again")
	}
	if s.conn != connOf(r) {
		// The whole point of the token: it says nothing on a connection other than the
		// one that proved who it was.
		return nil, errors.New("session used from another connection")
	}
	return s, nil
}

func (t *Transport) mailboxFor(node string) *mailbox {
	t.mu.Lock()
	defer t.mu.Unlock()

	box, ok := t.mailbox[node]
	if !ok {
		box = &mailbox{messages: make(chan wire.Envelope, t.mailboxSize)}
		t.mailbox[node] = box
	}
	return box
}

func bearer(r *http.Request) (string, error) {
	header := r.Header.Get(wire.HeaderAuthorization)
	scheme, value, ok := strings.Cut(header, " ")
	if !ok || scheme != wire.Scheme || value == "" {
		return "", fmt.Errorf("expected an %s authorization header", wire.Scheme)
	}
	return value, nil
}

func newToken() (string, error) {
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return "", fmt.Errorf("failed to read randomness for a session: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(token), nil
}

func write(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
