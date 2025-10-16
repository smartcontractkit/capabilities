package http

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/chainlink/v2/core/services/gateway"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/config"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/connector"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/network"

	httpcap "github.com/smartcontractkit/capabilities/http_action/action"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/ethereum/go-ethereum/crypto"

	httpclient "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"
)

const EthSignedMessagePrefix = "\x19Ethereum Signed Message:\n"

const nodeConfigTemplate = `
DonID = "test_don"
AuthMinChallengeLen = 32
AuthTimestampToleranceSec = 30
NodeAddress = "%s" 

[WsClientConfig]
HandshakeTimeoutMillis = 2_000

[[Gateways]]
Id = "test_gateway"
URL = "%s"
`

const gatewayConfigTemplate = `
{
  "ConnectionManagerConfig": {
    "AuthChallengeLen": 32,
    "AuthGatewayId": "test_gateway",
    "AuthTimestampToleranceSec": 30
  },
  "NodeServerConfig": {
    "Path": "/node",
    "Port": 0,
    "HandshakeTimeoutMillis": 2000,
    "MaxRequestBytes": 20000,
    "ReadTimeoutMillis": 1000,
    "RequestTimeoutMillis": 1000,
    "WriteTimeoutMillis": 1000
  },
  "UserServerConfig": {
    "Path": "/user",
    "Port": 0,
    "ContentTypeHeader": "application/jsonrpc",
    "MaxRequestBytes": 20000,
    "ReadTimeoutMillis": 1000,
    "RequestTimeoutMillis": 1000,
    "WriteTimeoutMillis": 1000
  },
  "Dons": [
    {
      "DonId": "test_don",
	  "F": 1,
      "Handlers": [
		{
			"Name": "http-capabilities",
			"ServiceName": "workflows",
			"Config": {
				"NodeRateLimiter": {
					"GlobalBurst": 50,
					"GlobalRPS": 50,
					"PerSenderBurst": 50,
					"PerSenderRPS": 50
				},
				"UserRateLimiter": {
					"GlobalBurst": 50,
					"GlobalRPS": 50,
					"PerSenderBurst": 50,
					"PerSenderRPS": 50	
				}
			}
		}
	  ],
      "Members": [
        {
          "Address": "%s",
          "Name": "test_node_1"
        }
      ]
    }
  ]
}
`

const serviceConfigTemplate = `
{
	"proxyMode": "gateway",
	"incomingRateLimiter": {
		"globalBurst": 50,
		"globalRPS": 50,
		"perSenderBurst": 50,
		"perSenderRPS": 50
	},
	"outgoingRateLimiter": {
		"globalBurst": 50,
		"globalRPS": 50,
		"perSenderBurst": 50,
		"perSenderRPS": 50
	}
}
`

func startTestHTTPServer(t *testing.T, handler http.Handler) (net.Listener, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		err := srv.Serve(listener)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Logf("HTTP server error: %v", err)
		}
	}()
	cleanup := func() { _ = srv.Close() }
	return listener, cleanup
}

func newTestNetworkClient(t *testing.T, addr *net.TCPAddr, lggr logger.Logger) network.HTTPClient {
	c, err := network.NewHTTPClient(network.HTTPClientConfig{
		DefaultTimeout:   5 * time.Second,
		MaxResponseBytes: 1000,
		AllowedPorts:     []int{addr.Port},
		AllowedIPs:       []string{addr.IP.String()},
	}, lggr)
	require.NoError(t, err)
	return c
}

func newTestGateway(t *testing.T, publicKey string, c network.HTTPClient, lggr logger.Logger) gateway.Gateway {
	gatewayConfigStr := fmt.Sprintf(gatewayConfigTemplate, publicKey)
	return newTestGatewayFromConfig(t, gatewayConfigStr, c, lggr)
}

func newTestGatewayFromConfig(t *testing.T, gatewayConfigStr string, c network.HTTPClient, lggr logger.Logger) gateway.Gateway {
	var gatewayConfig *config.GatewayConfig
	err := json.Unmarshal([]byte(gatewayConfigStr), &gatewayConfig)
	require.NoError(t, err)
	gateway, err := gateway.NewGatewayFromConfig(gatewayConfig, gateway.NewHandlerFactory(nil, nil, c, nil, nil, lggr, limits.Factory{Logger: lggr}), lggr)
	require.NoError(t, err)
	servicetest.Run(t, gateway)
	return gateway
}

func parseConnectorConfig(t *testing.T, tomlConfig string, nodeAddress string, nodeURL string) *connector.ConnectorConfig {
	nodeConfig := fmt.Sprintf(tomlConfig, nodeAddress, nodeURL)
	var cfg connector.ConnectorConfig
	require.NoError(t, toml.Unmarshal([]byte(nodeConfig), &cfg))
	return &cfg
}

func newTestGatewayConnector(t *testing.T, publicKey, nodeURL string, signer connector.Signer, lggr logger.Logger) core.GatewayConnector {
	cfg := parseConnectorConfig(t, nodeConfigTemplate, publicKey, nodeURL)
	gc, err := connector.NewGatewayConnector(cfg, signer, clockwork.NewRealClock(), lggr)
	require.NoError(t, err)
	servicetest.Run(t, gc)
	return gc
}

// Helper to create and initialize the HTTP capability
func newTestHTTPCapability(ctx context.Context, t *testing.T, gc core.GatewayConnector, lggr logger.Logger) server.ClientCapability {
	httpCapability := httpcap.NewService(lggr, limits.Factory{
		Logger: lggr,
		Meter:  beholder.GetMeter(),
	})
	err := httpCapability.Initialise(ctx, core.StandardCapabilitiesDependencies{
		Config:           serviceConfigTemplate,
		GatewayConnector: gc,
	})
	require.NoError(t, err)
	return httpCapability
}

// --- Test Client ---
type client struct {
	privateKey *ecdsa.PrivateKey
}

func (c *client) Sign(ctx context.Context, data ...[]byte) ([]byte, error) {
	var msg []byte
	for _, d := range data {
		msg = append(msg, d...)
	}
	prefixedMsg := fmt.Sprintf("%s%d%s", EthSignedMessagePrefix, len(msg), msg)
	hash := crypto.Keccak256Hash([]byte(prefixedMsg))
	return crypto.Sign(hash[:], c.privateKey)
}

func (c *client) ID() (string, error) {
	return "test_client", nil
}

func (*client) Start(ctx context.Context) error {
	return nil
}

func (*client) Close() error {
	return nil
}

// --- Main Test ---

func TestHTTPActionCapability(t *testing.T) {
	ctx := t.Context()
	lggr := logger.Test(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom-Header", "my-value")
		if _, err := io.WriteString(w, "pong"); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	})
	// random endpoint to test response caching
	mux.HandleFunc("/random", func(w http.ResponseWriter, r *http.Request) {
		randomID := uuid.New().String()
		w.Header().Set("X-Custom-Header", randomID)
		if _, err := io.WriteString(w, randomID); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	})
	// endpoint that returns a 404 error
	notFoundCounter := 0
	mux.HandleFunc("/not-found", func(w http.ResponseWriter, r *http.Request) {
		notFoundCounter++
		http.Error(w, "Not Found", http.StatusNotFound)
	})
	// endpoint that returns a 500 error with counter
	errorCounter := 0
	mux.HandleFunc("/error", func(w http.ResponseWriter, r *http.Request) {
		errorCounter++
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	})
	// POST endpoint to test request handling
	var postBody string
	var postHeaders http.Header
	mux.HandleFunc("/post", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		postBodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		postBody = string(postBodyBytes)
		postHeaders = r.Header
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "Post received"); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	})

	listener, cleanup := startTestHTTPServer(t, mux)
	t.Cleanup(cleanup)
	addr := listener.Addr().(*net.TCPAddr)

	netClient := newTestNetworkClient(t, addr, lggr)

	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	publicKey := strings.ToLower(crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
	gateway := newTestGateway(t, publicKey, netClient, lggr)
	nodeURL := fmt.Sprintf("ws://localhost:%d/node", gateway.GetNodePort())

	client := &client{privateKey: privateKey}
	gc := newTestGatewayConnector(t, publicKey, nodeURL, client, lggr)
	httpCapability := newTestHTTPCapability(ctx, t, gc, lggr)
	t.Run("GET /test returns pong with custom header", func(t *testing.T) {
		output, err := httpCapability.SendRequest(ctx, generateRandomRequestMetadata(), &httpclient.Request{
			Url:     fmt.Sprintf("http://%s/test", listener.Addr().String()),
			Method:  "GET",
			Headers: map[string]string{"X-Test": "1"},
			Body:    []byte("ping"),
		})
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(http.StatusOK), output.Response.StatusCode)
		require.Equal(t, "pong", string(output.Response.Body))
		require.Equal(t, "my-value", output.Response.Headers["X-Custom-Header"])
	})

	t.Run("POST /post with body and headers", func(t *testing.T) {
		input := &httpclient.Request{
			Url:     fmt.Sprintf("http://%s/post", listener.Addr().String()),
			Method:  "POST",
			Headers: map[string]string{"X-Test": "1"},
			Body:    []byte(`abc`),
		}
		output, err := httpCapability.SendRequest(ctx, generateRandomRequestMetadata(), input)
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(http.StatusOK), output.Response.StatusCode)
		require.Equal(t, "Post received", string(output.Response.Body))
		require.Equal(t, "1", postHeaders.Get("X-Test"))
		require.Equal(t, "abc", postBody)
	})

	t.Run("GET /random with caching enabled", func(t *testing.T) {
		requestMetadata := generateRandomRequestMetadata()
		initialOutput, err := httpCapability.SendRequest(ctx, requestMetadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/random", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				Store: true,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, initialOutput)
		require.Equal(t, uint32(http.StatusOK), initialOutput.Response.StatusCode)
		require.NotEmpty(t, initialOutput.Response.Body)
		require.NotEmpty(t, initialOutput.Response.Headers["X-Custom-Header"])

		cachedOutput, err := httpCapability.SendRequest(ctx, requestMetadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/random", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				MaxAge: durationpb.New(10000 * time.Millisecond),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, cachedOutput)
		require.Equal(t, uint32(http.StatusOK), cachedOutput.Response.StatusCode)
		require.NotEmpty(t, cachedOutput.Response.Body)
		require.NotEmpty(t, cachedOutput.Response.Headers["X-Custom-Header"])

		freshOutput, err := httpCapability.SendRequest(ctx, requestMetadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/random", listener.Addr().String()),
			Method: "GET",
		})
		require.NoError(t, err)
		require.NotNil(t, freshOutput)
		require.Equal(t, uint32(http.StatusOK), freshOutput.Response.StatusCode)
		require.NotEmpty(t, freshOutput.Response.Body)
		require.NotEmpty(t, freshOutput.Response.Headers["X-Custom-Header"])

		require.Equal(t, initialOutput.Response.Body, cachedOutput.Response.Body)
		require.Equal(t, initialOutput.Response.Headers["X-Custom-Header"], cachedOutput.Response.Headers["X-Custom-Header"])
		require.NotEqual(t, initialOutput.Response.Body, freshOutput.Response.Body)
		require.NotEqual(t, initialOutput.Response.Headers["X-Custom-Header"], freshOutput.Response.Headers["X-Custom-Header"])
	})

	t.Run("GET /not-found returns 404", func(t *testing.T) {
		requestMetadata := generateRandomRequestMetadata()
		output, err := httpCapability.SendRequest(ctx, requestMetadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/not-found", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				Store: true,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(http.StatusNotFound), output.Response.StatusCode)
		require.Equal(t, string(output.Response.Body), "Not Found\n")

		cachedOutput, err := httpCapability.SendRequest(ctx, requestMetadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/not-found", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				Store:  true,
				MaxAge: durationpb.New(10000 * time.Millisecond),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, cachedOutput)
		require.Equal(t, uint32(http.StatusNotFound), cachedOutput.Response.StatusCode)
		require.Equal(t, string(cachedOutput.Response.Body), "Not Found\n")
		require.Equal(t, notFoundCounter, 1, "not-found endpoint should have been called once")
	})

	t.Run("GET /error returns 500", func(t *testing.T) {
		output, err := httpCapability.SendRequest(ctx, generateRandomRequestMetadata(), &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/error", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				Store: true,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(http.StatusInternalServerError), output.Response.StatusCode)
		require.Equal(t, string(output.Response.Body), "Internal Server Error\n")

		cachedOutput, err := httpCapability.SendRequest(ctx, generateRandomRequestMetadata(), &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/error", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				Store:  true,
				MaxAge: durationpb.New(10000 * time.Millisecond),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, cachedOutput)
		require.Equal(t, uint32(http.StatusInternalServerError), cachedOutput.Response.StatusCode)
		require.Equal(t, string(cachedOutput.Response.Body), "Internal Server Error\n")
		require.Equal(t, errorCounter, 2, "error endpoint should have been called twice. No caching on 500")
	})

	t.Run("Caching works across workflows for same owner", func(t *testing.T) {
		workflow1Metadata := generateRandomRequestMetadata()
		initialOutput, err := httpCapability.SendRequest(ctx, workflow1Metadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/random", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				Store: true,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, initialOutput)
		require.Equal(t, uint32(http.StatusOK), initialOutput.Response.StatusCode)
		initialBody := string(initialOutput.Response.Body)
		initialHeader := initialOutput.Response.Headers["X-Custom-Header"]
		require.NotEmpty(t, initialBody)
		require.NotEmpty(t, initialHeader)

		// Second request: different workflow ID but same owner, should retrieve from cache
		workflow2Metadata := capabilities.RequestMetadata{
			WorkflowOwner:       workflow1Metadata.WorkflowOwner, // Same owner
			WorkflowID:          "0x2222222222222222222222222222222222222222222222222222222222222222",
			WorkflowExecutionID: fmt.Sprintf("execution_%s", uuid.New().String()),
		}

		cachedOutput, err := httpCapability.SendRequest(ctx, workflow2Metadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/random", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				MaxAge: durationpb.New(10000 * time.Millisecond),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, cachedOutput)
		require.Equal(t, uint32(http.StatusOK), cachedOutput.Response.StatusCode)

		// Verify cached response matches the first response (same random UUID)
		require.Equal(t, initialBody, string(cachedOutput.Response.Body),
			"Cache should work across different workflows for the same owner")
		require.Equal(t, initialHeader, cachedOutput.Response.Headers["X-Custom-Header"],
			"Cached headers should match across different workflows for the same owner")
	})

	t.Run("Caching does not work across different workflow owners", func(t *testing.T) {
		owner1Metadata := generateRandomRequestMetadata()
		firstOutput, err := httpCapability.SendRequest(ctx, owner1Metadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/random", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				Store: true,
			},
		})
		require.NoError(t, err)
		require.NotNil(t, firstOutput)
		require.Equal(t, uint32(http.StatusOK), firstOutput.Response.StatusCode)
		firstBody := string(firstOutput.Response.Body)
		firstHeader := firstOutput.Response.Headers["X-Custom-Header"]
		require.NotEmpty(t, firstBody)
		require.NotEmpty(t, firstHeader)

		owner2Metadata := generateRandomRequestMetadata()
		secondOutput, err := httpCapability.SendRequest(ctx, owner2Metadata, &httpclient.Request{
			Url:    fmt.Sprintf("http://%s/random", listener.Addr().String()),
			Method: "GET",
			CacheSettings: &httpclient.CacheSettings{
				MaxAge: durationpb.New(10000 * time.Millisecond),
			},
		})
		require.NoError(t, err)
		require.NotNil(t, secondOutput)
		require.Equal(t, uint32(http.StatusOK), secondOutput.Response.StatusCode)

		// Verify different response (fresh request, not cached)
		require.NotEqual(t, firstBody, string(secondOutput.Response.Body),
			"Cache should NOT work across different workflow owners - should get fresh response")
		require.NotEqual(t, firstHeader, secondOutput.Response.Headers["X-Custom-Header"],
			"Headers should be different for different workflow owners - not from cache")
	})
}

func generateRandomRequestMetadata() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		WorkflowOwner:       fmt.Sprintf("owner_%s", uuid.New().String()),
		WorkflowID:          fmt.Sprintf("workflow_%s", uuid.New().String()),
		WorkflowExecutionID: fmt.Sprintf("execution_%s", uuid.New().String()),
	}
}
