// Package connector is the node's end of the gateway connection.
//
// It implements core.GatewayConnector, the interface a node hands a capability
// today, so the capabilities did not have to change to move off the websocket -
// only this did. What replaces the socket is a kept-alive HTTP connection: see
// package wire.
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

type Config struct {
	// The gateway recognises it because the DON's membership lists it, and this
	// process proves it by signing with the matching key - which lives in crecore.
	NodeAddress string `json:"nodeAddress" usage:"the account this node authenticates to gateways as; its key is held by the keystore this process signs through"`

	DonID string `json:"donId" usage:"the DON this node belongs to, as its gateways know it"`

	// A node stays connected to all of them, and a request is answered by whichever
	// one asked.
	Gateways []string `json:"gateways" usage:"gateways to connect to, as id=url pairs" example:"['gateway_1=http://localhost:5002']"`

	ReceiveTimeout time.Duration `json:"receiveTimeout" usage:"how long a poll for gateway requests waits before returning empty"`
	RetryInterval  time.Duration `json:"retryInterval" usage:"how long to wait before reconnecting to a gateway that failed"`
}

var Defaults = Config{
	ReceiveTimeout: wire.DefaultReceiveTimeout,
	RetryInterval:  5 * time.Second,
}

type Connector struct {
	services.Service
	eng *services.Engine

	lggr   logger.Logger
	cfg    Config
	signer auth.Signer
	client *http.Client

	gateways map[string]string

	handlersMu sync.RWMutex
	handlers   map[string]core.GatewayConnectorHandler

	// connected closes as each gateway's first handshake succeeds, so that a caller
	// can wait for one.
	sessionsMu sync.RWMutex
	sessions   map[string]string
	connected  map[string]chan struct{}
}

var _ core.MultiGatewayConnector = (*Connector)(nil)

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
		// HTTP/2 because everything this node says to one gateway has to travel on one
		// connection: the gateway pins a session to the connection that proved who this
		// node is, and under HTTP/1.1 the long-poll would hold a connection while the
		// answers to it opened others.
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

// serve keeps one gateway connection alive, handshaking again when it breaks.
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

// handshake proves this node's identity: a signed header saying who and when,
// and a signature over the gateway's challenge.
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

	// Before signing: a node should not sign for a gateway it did not mean to reach.
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

// receive holds a request open until the gateway has something for this node.
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

	// Without holding the poll: the handler answers with a request of its own, and
	// this node should be waiting for the next message meanwhile.
	c.eng.Go(func(ctx context.Context) {
		if err := handler.HandleGatewayMessage(ctx, gatewayID, &request); err != nil {
			c.lggr.Errorw("Handler rejected a gateway message", "gateway", gatewayID, "method", request.Method, "err", err)
		}
	})
	return nil
}

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

// Everything a node says is a JSON-RPC response, including what starts an
// exchange: a request to fetch a URL is a response the gateway then answers with
// a request of its own. The shape the websocket carried, and the shape the
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

// SignMessage is for a capability that has something of its own to sign.
func (c *Connector) SignMessage(ctx context.Context, msg []byte) ([]byte, error) {
	return c.signer.Sign(ctx, auth.Hash(msg))
}

func (c *Connector) GatewayIDs(context.Context) ([]string, error) {
	return slices.Sorted(maps.Keys(c.gateways)), nil
}

// Every gateway this node has: a node in this shape belongs to one DON, and its
// gateways all serve it.
func (c *Connector) GatewayIDsForDon(ctx context.Context, _ string) ([]string, error) {
	return c.GatewayIDs(ctx)
}

func (c *Connector) DonID(context.Context) (string, error) { return c.cfg.DonID, nil }

func (c *Connector) DonIDForGateway(context.Context, string) (string, error) {
	return c.cfg.DonID, nil
}

func (c *Connector) PrimaryDonID(context.Context) (string, error) { return c.cfg.DonID, nil }

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

// poll reports whether the request came back with nothing, which is the ordinary
// outcome of a quiet minute rather than a failure.
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

// A plaintext gateway is reached with prior-knowledge HTTP/2 - h2c - because there
// is no TLS to negotiate the protocol with. Not a downgrade: identity is proved by
// signature over a challenge, so a readable hop costs nothing that matters here.
func transport() http.RoundTripper {
	plaintext := &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
	}
	return &schemeTransport{plaintext: plaintext, encrypted: &http2.Transport{}}
}

// https gets TLS and the protocol negotiated in it, http gets prior-knowledge
// HTTP/2.
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

// Tunnel is how the action capability reaches the internet when a workflow turned
// the cache off: the gateway opens the socket and carries the bytes, and the TLS
// the caller runs over it is between the caller and the far side.
//
// The proxy is on the same address as the control traffic, so a node that can talk
// to a gateway can tunnel through it with no second setting to keep in step.
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

// hostPort supplies the port a gateway's scheme implies when its URL names none.
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
