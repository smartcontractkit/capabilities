package main_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/http/gateway/auth"
	"github.com/smartcontractkit/capabilities/http/gateway/connector"
	"github.com/smartcontractkit/capabilities/http/gateway/service"
)

// proxy starts the tunnel half of a gateway and returns its address.
func proxy(t *testing.T, nodes ...node) string {
	t.Helper()

	addresses := make([]string, 0, len(nodes))
	for _, n := range nodes {
		addresses = append(addresses, n.address)
	}

	tunnel, err := service.NewTunnel(logger.Test(t), service.TunnelConfig{GatewayID: gatewayID}, auth.DONs{donID: addresses})
	require.NoError(t, err)

	// An ordinary HTTP/1.1 listener: a CONNECT takes the connection over, which is
	// the whole point, so there is nothing to multiplex.
	srv := httptest.NewServer(tunnel)
	t.Cleanup(srv.Close)

	return strings.TrimPrefix(srv.URL, "http://")
}

// TestTunnel is the case the cache being off leads to: the node reaches the far
// side itself, through a gateway that carries the bytes without reading them.
func TestTunnel(t *testing.T) {
	n := newNode(t)

	// The far side speaks TLS, as anything worth tunnelling to does. Its certificate
	// is what proves the node reached the right place - the gateway cannot stand in
	// the middle of that, which is the property being relied on.
	secret := "the body only the node and the origin should see"
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/secret", r.URL.Path)
		_, _ = w.Write([]byte(secret))
	}))
	t.Cleanup(origin.Close)

	tunnel := &connector.Tunnel{
		Gateway:   proxy(t, n),
		GatewayID: gatewayID,
		DonID:     donID,
		Signer:    auth.SignerFunc(n.Sign),
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: tunnel.DialContext,
		// The origin's own certificate, so the node is checking who it reached: this is
		// what a real client does with a real certificate authority.
		TLSClientConfig: &tls.Config{RootCAs: origin.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}, //#nosec G402 - the test's own CA
	}}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin.URL+"/secret", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, resp.Body.Close()) })

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, secret, string(body), "the node must reach the origin through the tunnel")
}

// TestTunnelRefusesStrangers is the membership check on the proxy: a key the DON
// does not list gets no tunnel, so the gateway cannot be used as open egress.
func TestTunnelRefusesStrangers(t *testing.T) {
	member, stranger := newNode(t), newNode(t)

	tunnel := &connector.Tunnel{
		Gateway:   proxy(t, member),
		GatewayID: gatewayID,
		DonID:     donID,
		Signer:    auth.SignerFunc(stranger.Sign),
	}

	_, err := tunnel.DialContext(t.Context(), "tcp", "example.com:443")
	require.ErrorContains(t, err, "refused a tunnel")
}

// TestTunnelRequiresTheChallenge is what makes the hop safe without TLS: a node
// that replays a captured header, and cannot sign what it is asked to sign, gets
// nowhere.
func TestTunnelRequiresTheChallenge(t *testing.T) {
	n := newNode(t)
	address := proxy(t, n)

	// A signer that produces the header and then refuses to answer the challenge -
	// which is exactly the position someone holding a copied header is in.
	var signed int
	replayer := auth.SignerFunc(func(ctx context.Context, hash []byte) ([]byte, error) {
		signed++
		if signed > 1 {
			return nil, assert.AnError
		}
		return n.Sign(ctx, hash)
	})

	tunnel := &connector.Tunnel{Gateway: address, GatewayID: gatewayID, DonID: donID, Signer: replayer}

	_, err := tunnel.DialContext(t.Context(), "tcp", "example.com:443")
	require.Error(t, err)
	assert.Equal(t, 2, signed, "the gateway must have asked for a challenge to be signed")
}

// TestTunnelNeedsAHostPort covers the shape of the request itself.
func TestTunnelNeedsAHostPort(t *testing.T) {
	n := newNode(t)
	address := proxy(t, n)

	conn, err := net.Dial("tcp", address)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, conn.Close()) })

	_, err = conn.Write([]byte("CONNECT / HTTP/1.1\r\nHost: \r\n\r\n"))
	require.NoError(t, err)

	answer := make([]byte, 15)
	_, err = conn.Read(answer)
	require.NoError(t, err)
	assert.Contains(t, string(answer), "407", "an unauthenticated CONNECT is challenged before anything else")
}

// TestTunnelSharesTheNodeListener is why a node needs one address for a gateway
// rather than two: the control traffic and the tunnels arrive on the same port.
// They can, because a tunnel is an HTTP/1.1 CONNECT that takes over its
// connection while the control traffic is HTTP/2 that multiplexes on one - and
// they should, because a second address is a second thing to configure wrongly.
func TestTunnelSharesTheNodeListener(t *testing.T) {
	n := newNode(t)
	// serve is the gateway as its binary runs it: node routes and the proxy on one
	// listener.
	url, _ := serve(t, &collector{}, n)

	// The control side: a node handshakes and is connected.
	connected := connect(t, url, n)
	require.Eventually(t, func() bool {
		return connected.AwaitConnection(t.Context(), gatewayID) == nil
	}, 5*time.Second, 20*time.Millisecond)

	// The tunnel side, on the same address, proved by the same key.
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("through the same port"))
	}))
	t.Cleanup(origin.Close)

	tunnel := &connector.Tunnel{
		Gateway:   strings.TrimPrefix(url, "http://"),
		GatewayID: gatewayID,
		DonID:     donID,
		Signer:    auth.SignerFunc(n.Sign),
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext:     tunnel.DialContext,
		TLSClientConfig: &tls.Config{RootCAs: origin.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}, //#nosec G402 - the test's own CA
	}}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, origin.URL+"/", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, resp.Body.Close()) })

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "through the same port", string(body))
}
