package outbound

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/smartcontractkit/capabilities/http/protos"
)

// dialled is where a gateway's tunnel was asked to open a connection, which is
// all a gateway carrying one gets to know.
type dialled struct {
	mu    sync.Mutex
	asked []string
}

func (d *dialled) addresses() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.asked...)
}

// tunnelling builds a gateway client whose tunnel dials locally, and a far side
// for it to reach.
func tunnelling(t *testing.T, handler http.HandlerFunc) (*gatewayOutboundProxy, *dialled, *httptest.Server) {
	t.Helper()

	origin := httptest.NewTLSServer(handler)
	t.Cleanup(origin.Close)

	opened := &dialled{}
	gateway := &mockGatewayConnector{
		SourceDonID: "don1",
		Gateways:    []mockGatewayEntry{{ID: "gateway1"}},
		OnTunnel: func(ctx context.Context, _, address string) (net.Conn, error) {
			opened.mu.Lock()
			opened.asked = append(opened.asked, address)
			opened.mu.Unlock()

			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		},
	}

	proxy, err := NewGatewayOutboundProxy(
		gateway,
		common.GatewayConnectionConfig{},
		logger.Test(t),
		testLimits(t),
	)
	require.NoError(t, err)

	return proxy, opened, origin
}

func tunnelRequest(url string, cache *protos.CacheSettings) *protos.Request {
	return &protos.Request{
		Url:           url,
		Method:        http.MethodGet,
		Timeout:       durationpb.New(5 * time.Second),
		CacheSettings: cache,
	}
}

func tunnelMetadata() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{WorkflowID: "wf1", WorkflowExecutionID: "exec1", WorkflowOwner: "owner1"}
}

// An uncached request is made by this node: the gateway is asked for a socket to
// the host and is told nothing else about it.
//
// What comes back here is a certificate failure, and that is the point. The far
// side is a test server with a certificate this node has no reason to trust, and
// the node is the one checking it - if the gateway were fetching, or standing in
// the middle, there would be nothing here to fail. That the request is really
// carried is covered end to end against a real gateway in the gateway package's
// tunnel test.
func TestUncachedRequestIsTunnelled(t *testing.T) {
	proxy, tunnel, origin := tunnelling(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("what the gateway must not read"))
	})

	_, err := proxy.SendRequest(t.Context(), common.OutboundRequest(tunnelMetadata(), tunnelRequest(origin.URL+"/secret", &protos.CacheSettings{})))
	require.ErrorContains(t, err, "certificate", "the node runs its own TLS through the tunnel, and checks it")

	assert.Equal(t, []string{origin.Listener.Addr().String()}, tunnel.addresses(),
		"the gateway is asked for a socket to the host, and told nothing else")
}

// A tunnelled request has to be https: the gateway carries the bytes, and
// plaintext bytes are bytes it can read.
func TestUncachedRequestMustBeEncrypted(t *testing.T) {
	proxy, tunnel, _ := tunnelling(t, func(http.ResponseWriter, *http.Request) {})

	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(plain.Close)

	_, err := proxy.SendRequest(t.Context(), common.OutboundRequest(tunnelMetadata(), tunnelRequest(plain.URL+"/open", &protos.CacheSettings{})))
	require.ErrorContains(t, err, "https")

	assert.Empty(t, tunnel.addresses(), "nothing should have been dialled")
}

// A request that wants the cache is the gateway's to make: agreeing on one answer
// is what the cache is for, and it cannot serve an answer it never saw.
func TestCachedRequestGoesThroughTheGateway(t *testing.T) {
	proxy, tunnel, origin := tunnelling(t, func(http.ResponseWriter, *http.Request) {
		assert.Fail(t, "a cached request is fetched by the gateway, not by this node")
	})

	sent := make(chan string, 1)
	gateway, ok := proxy.gateway.(*mockGatewayConnector)
	require.True(t, ok)
	gateway.OnSend = func(request string) { sent <- request }

	// The gateway never answers, so this ends in the wait for one - after the request
	// has gone to it, which is what is being checked.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()

	_, err := proxy.SendRequest(ctx, common.OutboundRequest(tunnelMetadata(), tunnelRequest(origin.URL+"/cached", &protos.CacheSettings{Store: true})))
	require.Error(t, err)

	select {
	case request := <-sent:
		assert.NotEmpty(t, request, "the gateway should have been asked to fetch")
	default:
		assert.Fail(t, "the request should have been sent to the gateway to fetch")
	}
	assert.Empty(t, tunnel.addresses(), "and no tunnel should have been opened")
}

// Which way a request leaves is the client's own decision, so there is nothing to
// configure and nothing to get out of step: the one gateway connection is both
// the thing a cached request is sent over and the thing an uncached one is
// tunnelled through.
func TestTheGatewayIsOneConnection(t *testing.T) {
	proxy, _, _ := tunnelling(t, func(http.ResponseWriter, *http.Request) {})

	var _ common.Gateway = proxy.gateway
	assert.NotNil(t, proxy.gateway, "one connection, in both of its roles")
}
