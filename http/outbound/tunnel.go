package outbound

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	gc "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

	"github.com/smartcontractkit/capabilities/http/common"
)

// maxTunnelledResponse is what is read back from a request this node made for
// itself when the workflow named no limit of its own. The same ceiling the
// gateway applies when it fetches.
const maxTunnelledResponse = 5 * 1024 * 1024

// wantsCache reports whether the request is one the gateway should fetch.
//
// A workflow that neither stores nor reads is a workflow that was never going to
// be served the same answer as its peers: each node's request is its own. So
// there is nothing for the gateway to add by making it, and something to lose -
// it would see the URL, the headers, the body and the answer. Those requests are
// tunnelled instead.
func wantsCache(settings gc.CacheSettings) bool {
	return settings.Store || settings.MaxAgeMs > 0
}

// tunnelled makes the request from this node, through the gateway's proxy.
//
// The gateway opens the socket to the far side and copies bytes between it and
// this node. The TLS runs here, so what the gateway can say about the request is
// its host, its timing and its size - not what was asked or what came back, and
// nothing it could alter without the far side's certificate.
func (p *gatewayOutboundProxy) tunnelled(
	ctx context.Context,
	lggr logger.Logger,
	gatewayID string,
	request gc.OutboundHTTPRequest,
) (gc.OutboundHTTPResponse, error) {
	// Plaintext through a tunnel would be plaintext the gateway can read, which is
	// the one thing this path exists to prevent. A workflow that turned the cache off
	// and asked for http:// has asked for two things that cannot both be true.
	if !strings.HasPrefix(strings.ToLower(request.URL), "https://") {
		p.metrics.IncrementInputValidationFailures(ctx, lggr)
		return gc.OutboundHTTPResponse{}, common.NewUserError(fmt.Errorf(
			"an uncached request goes out through a tunnel and has to be https, not %s: with the cache off the gateway carries bytes it cannot read, and plaintext would defeat that", request.URL))
	}

	client, err := p.tunnelClient(gatewayID, request)
	if err != nil {
		p.metrics.IncrementExecutionError(ctx, lggr)
		return gc.OutboundHTTPResponse{}, err
	}

	outbound, err := http.NewRequestWithContext(ctx, request.Method, request.URL, bytes.NewReader(request.Body))
	if err != nil {
		p.metrics.IncrementExecutionError(ctx, lggr)
		return gc.OutboundHTTPResponse{}, common.NewUserError(err)
	}
	outbound.Header = common.RequestHeaders(request)

	lggr.Debugw("tunnelling request through gateway", "selectedGateway", gatewayID, "url", request.URL)

	started := time.Now()
	answered, err := client.Do(outbound)
	if err != nil {
		// The far side, or the way to it: either way this is the request failing rather
		// than the capability, which is what the workflow has to be told.
		p.metrics.IncrementExternalEndpointError(ctx, lggr)
		return gc.OutboundHTTPResponse{}, common.NewUserError(fmt.Errorf("failed to reach %s: %w", request.URL, err))
	}
	defer func() { _ = answered.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(answered.Body, responseLimit(request)))
	if err != nil {
		p.metrics.IncrementExternalEndpointError(ctx, lggr)
		return gc.OutboundHTTPResponse{}, common.NewUserError(fmt.Errorf("failed to read what %s answered: %w", request.URL, err))
	}

	return common.ResponseOf(answered, body, time.Since(started)), nil
}

// responseLimit is how much of an answer is read: what the workflow asked for, or
// the same ceiling the gateway applies when it fetches.
func responseLimit(request gc.OutboundHTTPRequest) int64 {
	if request.MaxResponseBytes > 0 {
		return int64(request.MaxResponseBytes)
	}
	return maxTunnelledResponse
}

// tunnelClient is an HTTP client whose connections are opened by the gateway.
//
// Everything above the socket is this node's: the TLS handshake, the certificate
// check, and a client certificate when the workflow gave one - which the cached
// path cannot do at all, since the key would have to travel to the gateway.
func (p *gatewayOutboundProxy) tunnelClient(gatewayID string, request gc.OutboundHTTPRequest) (*http.Client, error) {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return p.gateway.Tunnel(ctx, gatewayID, address)
		},
		// One request, one connection: a tunnel is a socket the gateway opened for a
		// host this workflow named, and keeping it for the next request would keep it
		// for a request that may name another.
		DisableKeepAlives: true,
	}

	if request.Mtls != nil {
		certificate, err := tls.X509KeyPair([]byte(request.Mtls.Certificate), []byte(request.Mtls.PrivateKey))
		if err != nil {
			return nil, common.NewUserError(fmt.Errorf("the client certificate and key do not make a pair: %w", err))
		}
		transport.TLSClientConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
	}

	return &http.Client{
		Transport: transport,
		// Redirects are the far side's business and are followed as usual, but not into
		// another scheme: a tunnel was opened for one host, and http:// through it would
		// be plaintext the gateway could read.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after %d redirects", len(via))
			}
			if !strings.EqualFold(req.URL.Scheme, "https") {
				return fmt.Errorf("refusing a redirect to %s: a tunnelled request stays encrypted", req.URL.Scheme)
			}
			return nil
		},
	}, nil
}
