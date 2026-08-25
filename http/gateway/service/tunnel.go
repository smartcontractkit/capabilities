package service

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
)

// Tunnel is the gateway as a proxy: a node asks for a connection to a host, and
// the gateway carries bytes it cannot read.
//
// This is what a workflow gets when it turns the cache off. With the cache on,
// the gateway fetches on the DON's behalf and every node is served the same
// answer - which means the gateway sees the request and the response. With it
// off, each node was always going to get its own answer anyway, so there is
// nothing to be gained by the gateway being in the middle of it: it opens the
// socket and stands back, and the node runs its own TLS to the far side. The
// gateway learns the host and port, the timing and the byte counts. Not the
// content, and not enough to change it.
//
// Authentication is the handshake, in the place HTTP keeps it: the node's signed
// header in Proxy-Authorization, a challenge back in a 407, the answer in the
// retried CONNECT. The same two signatures as the control connection, so a
// captured header is worth nothing and the hop needs no TLS of its own to be
// safe.
type Tunnel struct {
	lggr      logger.Logger
	gatewayID string
	verifier  auth.Verifier
	tolerance time.Duration

	// dial opens the far side. It is a field so a test can watch where a tunnel went
	// without opening a socket to it.
	dial func(ctx context.Context, address string) (net.Conn, error)

	mu       sync.Mutex
	attempts map[string]*tunnelAttempt
}

type tunnelAttempt struct {
	donID     string
	node      string
	challenge []byte
	issued    time.Time
}

// TunnelConfig is what the proxy needs told.
type TunnelConfig struct {
	GatewayID          string
	TimestampTolerance time.Duration

	// DialTimeout bounds how long opening the far side may take.
	DialTimeout time.Duration
}

// NewTunnel returns the proxy half of a gateway.
func NewTunnel(lggr logger.Logger, cfg TunnelConfig, verifier auth.Verifier) (*Tunnel, error) {
	if cfg.GatewayID == "" {
		return nil, errors.New("a gateway needs an ID: it is what nodes sign their headers for")
	}
	if verifier == nil {
		return nil, errors.New("a proxy needs to know which nodes belong to which DON")
	}
	if cfg.TimestampTolerance <= 0 {
		cfg.TimestampTolerance = auth.DefaultTimestampTolerance
	}
	if cfg.DialTimeout <= 0 {
		cfg.DialTimeout = 30 * time.Second
	}

	dialer := &net.Dialer{Timeout: cfg.DialTimeout}
	return &Tunnel{
		lggr:      lggr,
		gatewayID: cfg.GatewayID,
		verifier:  verifier,
		tolerance: cfg.TimestampTolerance,
		dial: func(ctx context.Context, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		},
		attempts: map[string]*tunnelAttempt{},
	}, nil
}

// ServeHTTP answers CONNECT, and nothing else.
//
// A proxy that also served ordinary requests would be a proxy that could be asked
// to fetch something on a node's behalf without the DON agreeing to it, which is
// what the other half of this gateway is for.
func (t *Tunnel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.Error(w, "this proxy serves CONNECT", http.StatusMethodNotAllowed)
		return
	}

	node, err := t.authenticate(w, r)
	if err != nil {
		// Answered by authenticate: either a challenge, or a refusal.
		t.lggr.Debugw("Refused a tunnel", "err", err, "remote", r.RemoteAddr, "target", r.Host)
		return
	}

	target := r.Host
	if target == "" {
		http.Error(w, "CONNECT needs a host:port", http.StatusBadRequest)
		return
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		http.Error(w, "CONNECT needs a host:port", http.StatusBadRequest)
		return
	}

	upstream, err := t.dial(r.Context(), target)
	if err != nil {
		t.lggr.Warnw("Failed to open a tunnel", "node", node, "target", target, "err", err)
		http.Error(w, "failed to reach "+target, http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	client, err := hijack(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer client.Close()

	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		return
	}

	t.lggr.Infow("Tunnel open", "node", node, "target", target)
	pipe(client, upstream)
	t.lggr.Debugw("Tunnel closed", "node", node, "target", target)
}

// authenticate is the handshake, in two CONNECTs.
//
// The first carries the node's signed header and is answered with 407 and a
// challenge; the second carries the answer. That is HTTP's own challenge
// mechanism, so an ordinary proxy client understands the shape of it even though
// the scheme is ours.
func (t *Tunnel) authenticate(w http.ResponseWriter, r *http.Request) (string, error) {
	scheme, value, ok := strings.Cut(r.Header.Get("Proxy-Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, wireScheme) {
		return "", t.challengeless(w, "a "+wireScheme+" proxy authorization is required")
	}

	credentials, err := parseCredentials(value)
	if err != nil {
		return "", t.challengeless(w, err.Error())
	}

	header, err := base64.StdEncoding.DecodeString(credentials["header"])
	if err != nil {
		return "", t.challengeless(w, "the header is not base64")
	}

	claimed, node, err := auth.VerifyHeader(t.verifier, t.gatewayID, header, time.Now(), t.tolerance)
	if err != nil {
		return "", t.challengeless(w, err.Error())
	}

	// No answer yet: issue a challenge and let the client come back with it. This is
	// the round trip that makes a captured header useless.
	answer, answering := credentials["response"]
	if !answering {
		return "", t.challenge(w, claimed.DonID, node)
	}

	attemptID := credentials["attempt"]
	t.mu.Lock()
	attempt, known := t.attempts[attemptID]
	delete(t.attempts, attemptID)
	t.mu.Unlock()

	if !known {
		return "", t.challenge(w, claimed.DonID, node)
	}
	if attempt.node != node || attempt.donID != claimed.DonID {
		return "", t.challengeless(w, "the challenge was issued to another node")
	}
	if time.Since(attempt.issued) > t.tolerance {
		return "", t.challenge(w, claimed.DonID, node)
	}

	signature, err := base64.StdEncoding.DecodeString(answer)
	if err != nil {
		return "", t.challengeless(w, "the response is not base64")
	}
	if err := auth.VerifyChallengeResponse(t.verifier, attempt.donID, attempt.node, attempt.challenge, signature); err != nil {
		return "", t.challengeless(w, err.Error())
	}

	return node, nil
}

// challenge answers with 407 and something to sign.
func (t *Tunnel) challenge(w http.ResponseWriter, donID, node string) error {
	challenge, err := auth.NewChallenge(t.gatewayID, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}
	packed, err := auth.PackChallenge(challenge)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return err
	}

	id := uuid.NewString()
	t.mu.Lock()
	t.attempts[id] = &tunnelAttempt{donID: donID, node: node, challenge: packed, issued: time.Now()}
	t.mu.Unlock()

	w.Header().Set("Proxy-Authenticate", fmt.Sprintf(`%s attempt="%s", challenge="%s"`,
		wireScheme, id, base64.StdEncoding.EncodeToString(packed)))
	w.WriteHeader(http.StatusProxyAuthRequired)

	return errors.New("challenged")
}

// challengeless refuses without offering a challenge: whatever was wrong is not
// something another round trip would fix.
func (t *Tunnel) challengeless(w http.ResponseWriter, reason string) error {
	http.Error(w, reason, http.StatusProxyAuthRequired)
	return errors.New(reason)
}

// parseCredentials reads the comma-separated key="value" pairs a proxy
// authorization carries.
func parseCredentials(value string) (map[string]string, error) {
	credentials := map[string]string{}
	for _, pair := range strings.Split(value, ",") {
		key, raw, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok {
			return nil, errors.New("proxy authorization is not a list of key=value pairs")
		}
		credentials[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(raw), `"`)
	}
	if credentials["header"] == "" {
		return nil, errors.New("proxy authorization carries no header")
	}
	return credentials, nil
}

// hijack takes the connection out of the server's hands, which is what a tunnel
// is: after this there is no HTTP left, only bytes.
func hijack(w http.ResponseWriter) (net.Conn, error) {
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("this server cannot hand over a connection to tunnel with")
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	if buffered.Reader.Buffered() > 0 {
		// The client spoke before it was answered. Nothing said before "200 Connection
		// established" belongs to the tunnel, and passing it on would be passing on
		// something that was not part of it.
		conn.Close()
		return nil, errors.New("the client sent data before the tunnel was established")
	}
	return conn, nil
}

// pipe carries bytes both ways until either side stops.
func pipe(client, upstream net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	copy := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)

		// Half-closed rather than closed, so that the other direction can finish: a
		// request that has been sent still deserves its answer.
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
			return
		}
		_ = dst.Close()
	}

	go copy(upstream, client)
	go copy(client, upstream)
	wg.Wait()
}

// wireScheme is the authorization scheme, the same word the control connection
// uses.
const wireScheme = "CRE"
