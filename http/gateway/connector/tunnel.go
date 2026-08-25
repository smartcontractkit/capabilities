package connector

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
)

// Tunnel dials through a gateway's proxy, so that what travels is between this
// node and the far side and nothing in between.
//
// It is the other end of service.Tunnel: a CONNECT with this node's signed
// header, a 407 carrying a challenge, and the same CONNECT again with the
// challenge signed. Two signatures, the same two the control connection makes -
// which is what lets the hop to the gateway be plaintext without a captured
// header being worth anything.
type Tunnel struct {
	// Gateway is the proxy's address, host:port.
	Gateway string

	// GatewayID, DonID and Signer are this node's side of the handshake, the same
	// three the control connection uses.
	GatewayID string
	DonID     string
	Signer    auth.Signer

	// Dial opens the hop to the gateway. Nil dials TCP, which is what a caller
	// wants unless it is a test.
	Dial func(ctx context.Context, address string) (net.Conn, error)
}

// DialContext opens a connection to address through the gateway.
//
// It is shaped to be an http.Transport's DialContext, which is how a node's HTTP
// client is pointed at the proxy: the client then runs its own TLS through the
// tunnel, and the gateway carries bytes it cannot read.
func (t *Tunnel) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	dial := t.Dial
	if dial == nil {
		dialer := &net.Dialer{}
		dial = func(ctx context.Context, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, "tcp", address)
		}
	}

	conn, err := dial(ctx, t.Gateway)
	if err != nil {
		return nil, fmt.Errorf("failed to reach the gateway proxy at %s: %w", t.Gateway, err)
	}

	if err := t.handshake(ctx, conn, address); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// handshake is the two CONNECTs.
func (t *Tunnel) handshake(ctx context.Context, conn net.Conn, address string) error {
	header, err := t.header()
	if err != nil {
		return err
	}

	reader := bufio.NewReader(conn)

	// The first CONNECT carries who this node is; a gateway that has not seen it
	// before answers with something to sign.
	resp, err := t.connect(ctx, conn, reader, address, credentials(header, "", ""))
	if err != nil {
		return err
	}

	if resp.StatusCode == http.StatusOK {
		// A gateway that asked for no challenge is not one this node should be talking
		// to: without it, the header it just sent could be replayed by anyone who saw it.
		return errors.New("the gateway accepted a tunnel without a challenge")
	}
	if resp.StatusCode != http.StatusProxyAuthRequired {
		return fmt.Errorf("the gateway refused a tunnel to %s: %s", address, resp.Status)
	}

	// A 407 with nothing to sign is a refusal rather than a challenge: whatever was
	// wrong with the header - an unknown node, a stale timestamp - another round trip
	// would not fix.
	authenticate := resp.Header.Get("Proxy-Authenticate")
	if authenticate == "" {
		return fmt.Errorf("the gateway refused a tunnel to %s: %s", address, refusal(resp))
	}

	attempt, challenge, err := parseChallenge(authenticate)
	if err != nil {
		return err
	}

	answer, err := t.Signer.Sign(ctx, auth.Hash(challenge))
	if err != nil {
		return fmt.Errorf("failed to sign the gateway's challenge: %w", err)
	}

	resp, err = t.connect(ctx, conn, reader, address, credentials(header, attempt, base64.StdEncoding.EncodeToString(answer)))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the gateway refused a tunnel to %s: %s", address, resp.Status)
	}
	if reader.Buffered() > 0 {
		return errors.New("the gateway sent data before the tunnel was established")
	}
	return nil
}

// connect writes one CONNECT and reads its answer.
func (t *Tunnel) connect(ctx context.Context, conn net.Conn, reader *bufio.Reader, address, authorization string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodConnect, "http://"+address, nil)
	if err != nil {
		return nil, err
	}
	req.Host = address
	req.Header.Set("Proxy-Authorization", authorization)

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
		defer func() { _ = conn.SetDeadline(time.Time{}) }()
	}

	if err := req.Write(conn); err != nil {
		return nil, fmt.Errorf("failed to send CONNECT: %w", err)
	}

	resp, err := http.ReadResponse(reader, req)
	if err != nil {
		return nil, fmt.Errorf("failed to read the gateway's answer to CONNECT: %w", err)
	}
	return resp, nil
}

// refusal is what the gateway said about why, for an error a caller can act on.
func refusal(resp *http.Response) string {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	if reason := strings.TrimSpace(string(body)); reason != "" {
		return resp.Status + ": " + reason
	}
	return resp.Status
}

// header is this node's signed claim about itself, the same bytes the control
// connection sends.
func (t *Tunnel) header() (string, error) {
	packed, err := auth.PackHeader(auth.Header{
		Timestamp: uint32(time.Now().Unix()), //#nosec G115 - seconds since the epoch, until 2106
		DonID:     t.DonID,
		GatewayID: t.GatewayID,
	})
	if err != nil {
		return "", err
	}

	signature, err := t.Signer.Sign(context.Background(), auth.Hash(packed))
	if err != nil {
		return "", fmt.Errorf("failed to sign the auth header: %w", err)
	}
	return base64.StdEncoding.EncodeToString(append(packed, signature...)), nil
}

func credentials(header, attempt, response string) string {
	pairs := []string{fmt.Sprintf(`header="%s"`, header)}
	if attempt != "" {
		pairs = append(pairs, fmt.Sprintf(`attempt="%s"`, attempt))
	}
	if response != "" {
		pairs = append(pairs, fmt.Sprintf(`response="%s"`, response))
	}
	return "CRE " + strings.Join(pairs, ", ")
}

// parseChallenge reads what the gateway wants signed.
func parseChallenge(header string) (string, []byte, error) {
	scheme, value, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "CRE") {
		return "", nil, fmt.Errorf("the gateway asked for %q authentication, which this node does not do", scheme)
	}

	fields := map[string]string{}
	for _, pair := range strings.Split(value, ",") {
		key, raw, found := strings.Cut(strings.TrimSpace(pair), "=")
		if !found {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(raw), `"`)
	}

	challenge, err := base64.StdEncoding.DecodeString(fields["challenge"])
	if err != nil || len(challenge) == 0 {
		return "", nil, errors.New("the gateway's challenge is not base64")
	}
	if fields["attempt"] == "" {
		return "", nil, errors.New("the gateway's challenge names no attempt")
	}
	return fields["attempt"], challenge, nil
}
