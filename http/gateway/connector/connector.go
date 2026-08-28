// Package connector is the node's end of the gateway connection.
//
// It is what the capabilities in this binary are handed: they add handlers for
// the JSON-RPC methods they answer, and they send messages back. That interface
// (core.GatewayConnector) is the one a node hands a capability today, so the
// capabilities did not have to change to move off the websocket - only this did.
//
// What replaces the websocket is a kept-alive HTTP connection carrying three
// kinds of request: the handshake that proves who this node is, a long-poll that
// the gateway answers when it has something for this node, and a post for what
// this node has to say back. See package wire.
package connector

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	neturl "net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	"github.com/smartcontractkit/capabilities/http/gateway/wire"
)

// Config is where the gateways are and who this node says it is.
type Config struct {
	// NodeAddress is the account this node is known by, 0x-prefixed. The gateway
	// recognises it because the DON's membership lists it, and this process proves it
	// by signing with the matching key - which lives in crecore, not here.
	NodeAddress string `json:"nodeAddress" usage:"the account this node authenticates to gateways as; its key is held by the keystore this process signs through"`

	// DonID is the DON this node belongs to, as the gateway knows it.
	DonID string `json:"donId" usage:"the DON this node belongs to, as its gateways know it"`

	// Gateways are the gateways to connect to, as id=url pairs. A node stays
	// connected to all of them, and a request is answered by whichever one asked.
	Gateways []string `json:"gateways" usage:"gateways to connect to, as id=url pairs" example:"['gateway_1=http://localhost:5002']"`

	// ReceiveTimeout is how long a long-poll waits before coming back empty, and
	// RetryInterval how long to wait after a failed attempt before trying again.
	ReceiveTimeout time.Duration `json:"receiveTimeout" usage:"how long a poll for gateway requests waits before returning empty"`
	RetryInterval  time.Duration `json:"retryInterval" usage:"how long to wait before reconnecting to a gateway that failed"`
}

// Defaults are what a node that says nothing else gets.
var Defaults = Config{
	ReceiveTimeout: wire.DefaultReceiveTimeout,
	RetryInterval:  5 * time.Second,
}

// Connector is the node's connection to its gateways.
type Connector struct {
	services.Service
	eng *services.Engine

	lggr   logger.Logger
	cfg    Config
	signer auth.Signer
	client *http.Client

	// gateways is id -> base URL, from Config.Gateways.
	gateways map[string]string

	// handlers is method -> handler, added by the capabilities as they start.
	handlersMu sync.RWMutex
	handlers   map[string]core.GatewayConnectorHandler

	// sessions is gatewayID -> the token that connection is authenticated by, and
	// connected closes as each gateway's first handshake succeeds so that a caller
	// can wait for one.
	sessionsMu sync.RWMutex
	sessions   map[string]string
	connected  map[string]chan struct{}
}

var _ core.MultiGatewayConnector = (*Connector)(nil)

// New returns the connector, not yet connected.
func New(lggr logger.Logger, cfg Config, signer auth.Signer) (*Connector, error) {
	if cfg.NodeAddress == "" {
		return nil, errors.New("a gateway connector needs --gateway.node-address: it is the identity the gateway knows this node by")
	}
	if cfg.DonID == "" {
		return nil, errors.New("a gateway connector needs --gateway.don-id")
	}
	if signer == nil {
		return nil, errors.New("a gateway connector needs something to sign with")
	}

	gateways, err := parseGateways(cfg.Gateways)
	if err != nil {
		return nil, err
	}
	if len(gateways) == 0 {
		return nil, errors.New("a gateway connector needs at least one --gateway.gateways entry, as id=url")
	}

	if cfg.ReceiveTimeout <= 0 {
		cfg.ReceiveTimeout = Defaults.ReceiveTimeout
	}
	if cfg.RetryInterval <= 0 {
		cfg.RetryInterval = Defaults.RetryInterval
	}

	c := &Connector{
		lggr:     lggr,
		cfg:      cfg,
		signer:   signer,
		gateways: gateways,
		handlers: map[string]core.GatewayConnectorHandler{},
		sessions: map[string]string{},
		connected: func() map[string]chan struct{} {
			waiters := make(map[string]chan struct{}, len(gateways))
			for id := range gateways {
				waiters[id] = make(chan struct{})
			}
			return waiters
		}(),
		// HTTP/2, and its own transport.
		//
		// HTTP/2 because everything this node says to one gateway has to travel on one
		// connection: the gateway pins a session to the connection that proved who this
		// node is, and under HTTP/1.1 the long-poll would hold a connection while the
		// answers to it opened others. Multiplexed streams make "one connection" true
		// again without holding anything up.
		//
		// Its own, because the default transport is shared by everything in the process -
		// so two connectors would share connections, and with them each other's sessions.
		client: &http.Client{Transport: transport()},
	}

	c.Service, c.eng = services.Config{
		Name:  "GatewayConnector",
		Start: c.start,
	}.NewServiceEngine(lggr)

	return c, nil
}

func (c *Connector) start(context.Context) error {
	for id := range c.gateways {
		c.eng.Go(func(ctx context.Context) { c.serve(ctx, id) })
	}
	return nil
}

// serve keeps one gateway connection alive: handshake, then poll for work until
// something breaks, then handshake again.
func (c *Connector) serve(ctx context.Context, gatewayID string) {
	for ctx.Err() == nil {
		if err := c.session(ctx, gatewayID); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.lggr.Warnw("Gateway connection ended, reconnecting", "gateway", gatewayID, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.cfg.RetryInterval):
			}
		}
	}
}

// session handshakes and then polls until the connection fails.
func (c *Connector) session(ctx context.Context, gatewayID string) error {
	token, err := c.handshake(ctx, gatewayID)
	if err != nil {
		return fmt.Errorf("handshake failed: %w", err)
	}

	c.sessionsMu.Lock()
	c.sessions[gatewayID] = token
	waiter, waiting := c.connected[gatewayID]
	c.sessionsMu.Unlock()
	if waiting {
		select {
		case <-waiter:
		default:
			close(waiter)
		}
	}

	c.lggr.Infow("Connected to gateway", "gateway", gatewayID, "node", c.cfg.NodeAddress, "don", c.cfg.DonID)

	for ctx.Err() == nil {
		if err := c.receive(ctx, gatewayID, token); err != nil {
			return err
		}
	}
	return ctx.Err()
}

// handshake is the two round trips that prove this node's identity: a signed
// header saying who and when, and a signature over the gateway's challenge.
func (c *Connector) handshake(ctx context.Context, gatewayID string) (string, error) {
	header, err := auth.PackHeader(auth.Header{
		Timestamp: uint32(time.Now().Unix()), //#nosec G115 - seconds since the epoch, until 2106
		DonID:     c.cfg.DonID,
		GatewayID: gatewayID,
	})
	if err != nil {
		return "", err
	}
	signature, err := c.signer.Sign(ctx, auth.Hash(header))
	if err != nil {
		return "", fmt.Errorf("failed to sign the auth header: %w", err)
	}

	var reply wire.ConnectReply
	if err := c.post(ctx, gatewayID, wire.PathConnect, authorization(append(header, signature...)), nil, &reply); err != nil {
		return "", err
	}

	// Checked before signing: the challenge names the gateway that issued it, and a
	// node should not sign for a gateway it did not mean to talk to.
	challenge, err := auth.UnpackChallenge(reply.Challenge)
	if err != nil {
		return "", err
	}
	if challenge.GatewayID != gatewayID {
		return "", fmt.Errorf("gateway %q sent a challenge for %q", gatewayID, challenge.GatewayID)
	}

	answer, err := c.signer.Sign(ctx, auth.Hash(reply.Challenge))
	if err != nil {
		return "", fmt.Errorf("failed to sign the gateway's challenge: %w", err)
	}

	var finished wire.FinishReply
	body := wire.FinishRequest{AttemptID: reply.AttemptID, Signature: answer}
	if err := c.post(ctx, gatewayID, wire.PathFinish, "", body, &finished); err != nil {
		return "", err
	}
	if finished.Token == "" {
		return "", errors.New("the gateway accepted the handshake without issuing a session")
	}
	return finished.Token, nil
}

// receive holds a request open until the gateway has something for this node,
// then hands it to whichever handler registered that method.
func (c *Connector) receive(ctx context.Context, gatewayID, token string) error {
	var envelope wire.Envelope
	empty, err := c.poll(ctx, gatewayID, token, &envelope)
	if err != nil || empty {
		return err
	}

	var request jsonrpc.Request[json.RawMessage]
	if err := json.Unmarshal(envelope.Message, &request); err != nil {
		c.lggr.Errorw("Gateway sent something that is not a JSON-RPC request", "gateway", gatewayID, "err", err)
		return nil
	}

	c.handlersMu.RLock()
	handler, ok := c.handlers[request.Method]
	c.handlersMu.RUnlock()
	if !ok {
		c.lggr.Warnw("No handler for a method the gateway sent", "gateway", gatewayID, "method", request.Method)
		return nil
	}

	// Handled without holding the poll: the handler answers by sending, which is its
	// own request, and this node should be waiting for the next message meanwhile.
	c.eng.Go(func(ctx context.Context) {
		if err := handler.HandleGatewayMessage(ctx, gatewayID, &request); err != nil {
			c.lggr.Errorw("Handler rejected a gateway message", "gateway", gatewayID, "method", request.Method, "err", err)
		}
	})
	return nil
}

// AddHandler registers a handler for the methods it answers.
func (c *Connector) AddHandler(_ context.Context, methods []string, handler core.GatewayConnectorHandler) error {
	if handler == nil {
		return errors.New("a handler is required")
	}

	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()

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
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()

	for _, method := range methods {
		delete(c.handlers, method)
	}
	return nil
}

// SendToGateway sends a signed message to one gateway.
//
// Everything a node says is a JSON-RPC response, including what starts an
// exchange: a request to fetch a URL is a response the gateway then answers with
// a request of its own. That is the shape the websocket carried and the shape the
// capabilities are written against.
func (c *Connector) SendToGateway(ctx context.Context, gatewayID string, resp *jsonrpc.Response[json.RawMessage]) error {
	token, err := c.token(ctx, gatewayID)
	if err != nil {
		return err
	}

	message, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to encode the message: %w", err)
	}
	return c.post(ctx, gatewayID, wire.PathSend, token, wire.Envelope{GatewayID: gatewayID, Message: message}, nil)
}

// SignMessage signs on this node's behalf, for a capability that has something of
// its own to sign.
func (c *Connector) SignMessage(ctx context.Context, msg []byte) ([]byte, error) {
	return c.signer.Sign(ctx, auth.Hash(msg))
}

func (c *Connector) GatewayIDs(context.Context) ([]string, error) {
	return slices.Sorted(maps.Keys(c.gateways)), nil
}

// GatewayIDsForDon is every gateway this node has: a node in this shape belongs
// to one DON, and its gateways all serve it.
func (c *Connector) GatewayIDsForDon(ctx context.Context, _ string) ([]string, error) {
	return c.GatewayIDs(ctx)
}

func (c *Connector) DonID(context.Context) (string, error) { return c.cfg.DonID, nil }

func (c *Connector) DonIDForGateway(context.Context, string) (string, error) {
	return c.cfg.DonID, nil
}

func (c *Connector) PrimaryDonID(context.Context) (string, error) { return c.cfg.DonID, nil }

// AwaitConnection waits until this node has handshaked with gatewayID.
func (c *Connector) AwaitConnection(ctx context.Context, gatewayID string) error {
	c.sessionsMu.RLock()
	waiter, known := c.connected[gatewayID]
	c.sessionsMu.RUnlock()
	if !known {
		return fmt.Errorf("gateway %q is not one this node connects to", gatewayID)
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-waiter:
		return nil
	}
}

// token is the session token for a gateway this node has already handshaked with.
func (c *Connector) token(ctx context.Context, gatewayID string) (string, error) {
	c.sessionsMu.RLock()
	token, ok := c.sessions[gatewayID]
	c.sessionsMu.RUnlock()
	if ok {
		return token, nil
	}

	if err := c.AwaitConnection(ctx, gatewayID); err != nil {
		return "", err
	}
	c.sessionsMu.RLock()
	defer c.sessionsMu.RUnlock()
	return c.sessions[gatewayID], nil
}

// post sends one request to a gateway, with an optional body and reply.
func (c *Connector) post(ctx context.Context, gatewayID, path, authorization string, body, reply any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to encode the request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.gateways[gatewayID]+path, payload)
	if err != nil {
		return err
	}
	if authorization != "" {
		req.Header.Set(wire.HeaderAuthorization, wire.Scheme+" "+authorization)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return statusError(gatewayID, path, resp)
	}
	if reply == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(reply)
}

// poll holds a request open until the gateway has a message, and reports whether
// it came back with nothing - which is the ordinary outcome of a quiet minute,
// not a failure.
func (c *Connector) poll(ctx context.Context, gatewayID, token string, envelope *wire.Envelope) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.ReceiveTimeout*2)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.gateways[gatewayID]+wire.PathReceive, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set(wire.HeaderAuthorization, wire.Scheme+" "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusOK:
		return false, json.NewDecoder(resp.Body).Decode(envelope)
	default:
		return false, statusError(gatewayID, wire.PathReceive, resp)
	}
}

// transport dials one connection per gateway and multiplexes over it, whether or
// not the hop is encrypted.
//
// A plaintext gateway is reached with prior-knowledge HTTP/2 - h2c - because
// there is no TLS to negotiate the protocol with. That is not a downgrade: the
// handshake proves identity by signature over a challenge, so the hop being
// readable costs the same as it does for the proxy tunnel, which is nothing that
// matters here.
func transport() http.RoundTripper {
	plaintext := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	return &schemeTransport{plaintext: plaintext, encrypted: &http2.Transport{}}
}

// schemeTransport picks the dialling by the URL: https gets TLS and the protocol
// negotiated in it, http gets prior-knowledge HTTP/2.
type schemeTransport struct {
	plaintext *http2.Transport
	encrypted *http2.Transport
}

func (t *schemeTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme == "https" {
		return t.encrypted.RoundTrip(r)
	}
	return t.plaintext.RoundTrip(r)
}

func statusError(gatewayID, path string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("gateway %s answered %s with %s: %s", gatewayID, path, resp.Status, bytes.TrimSpace(body))
}

func authorization(header []byte) string {
	return base64.StdEncoding.EncodeToString(header)
}

// parseGateways reads the id=url pairs a node is configured with.
func parseGateways(pairs []string) (map[string]string, error) {
	gateways := make(map[string]string, len(pairs))
	for _, pair := range pairs {
		id, url, ok := strings.Cut(pair, "=")
		if !ok || id == "" || url == "" {
			return nil, fmt.Errorf("gateway %q is not id=url", pair)
		}
		gateways[id] = strings.TrimSuffix(url, "/")
	}
	return gateways, nil
}

// Tunnel opens a connection to address through gatewayID's proxy.
//
// It is how the action capability reaches the internet when a workflow turned the
// cache off: the gateway opens the socket and carries the bytes, and the TLS the
// caller runs over what comes back is between it and the far side. The proxy is
// on the same address as the control traffic - see the gateway's node listener -
// so a node that can talk to a gateway can tunnel through it, with no second
// setting to keep in step with the first.
func (c *Connector) Tunnel(ctx context.Context, gatewayID, address string) (net.Conn, error) {
	url, known := c.gateways[gatewayID]
	if !known {
		return nil, fmt.Errorf("gateway %q is not one this node connects to", gatewayID)
	}

	proxy, err := hostPort(url)
	if err != nil {
		return nil, err
	}

	tunnel := &Tunnel{
		Gateway:   proxy,
		GatewayID: gatewayID,
		DonID:     c.cfg.DonID,
		Signer:    c.signer,
	}
	return tunnel.DialContext(ctx, "tcp", address)
}

// hostPort is the address to dial for a gateway's URL, with the port its scheme
// implies when it names none.
func hostPort(gateway string) (string, error) {
	parsed, err := neturl.Parse(gateway)
	if err != nil {
		return "", fmt.Errorf("gateway URL %q cannot be parsed: %w", gateway, err)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("gateway URL %q names no host", gateway)
	}
	if parsed.Port() != "" {
		return parsed.Host, nil
	}

	switch parsed.Scheme {
	case "https":
		return net.JoinHostPort(parsed.Host, "443"), nil
	default:
		return net.JoinHostPort(parsed.Host, "80"), nil
	}
}
