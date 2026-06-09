package http

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/jonboulle/clockwork"
	"github.com/stretchr/testify/require"

	httpcap "github.com/smartcontractkit/capabilities/http_action/action"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	httpclient "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http/server"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/connector"
)

const (
	multiDonTestOrgID = "multi-don-test-org"
	gatewayDonUS      = "gateway_don_us"
	gatewayDonEU      = "gateway_don_eu"
)

const multiDonNodeConfigTemplate = `
DonID = "workflow_don"
AuthMinChallengeLen = 32
AuthTimestampToleranceSec = 30
NodeAddress = "%s"

[WsClientConfig]
HandshakeTimeoutMillis = 2_000

[[Gateways]]
Id = "gateway_us"
DonId = "gateway_don_us"
URL = "%s"

[[Gateways]]
Id = "gateway_eu"
DonId = "gateway_don_eu"
URL = "%s"
`

func multiDonGatewayConfig(gatewayID, publicKey string) string {
	return fmt.Sprintf(`{
  "ConnectionManagerConfig": {
    "AuthChallengeLen": 32,
    "AuthGatewayId": %q,
    "AuthTimestampToleranceSec": 30
  },
  "NodeServerConfig": {
    "Path": "/node",
    "Port": 0,
    "HandshakeTimeoutMillis": 2000,
    "MaxRequestBytes": 20000,
    "ReadTimeoutMillis": 5000,
    "RequestTimeoutMillis": 5000,
    "WriteTimeoutMillis": 10000
  },
  "UserServerConfig": {
    "Path": "/user",
    "Port": 0,
    "ContentTypeHeader": "application/jsonrpc",
    "MaxRequestBytes": 20000,
    "ReadTimeoutMillis": 5000,
    "RequestTimeoutMillis": 5000,
    "WriteTimeoutMillis": 10000
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
          "Address": %q,
          "Name": "test_node_1"
        }
      ]
    }
  ]
}`, gatewayID, publicKey)
}

type recordingGatewayConnector struct {
	core.GatewayConnector
	lastGatewayID string
}

func (r *recordingGatewayConnector) SendToGateway(ctx context.Context, gatewayID string, resp *jsonrpc.Response[json.RawMessage]) error {
	r.lastGatewayID = gatewayID
	return r.GatewayConnector.SendToGateway(ctx, gatewayID, resp)
}

type multiDonRoutingEnv struct {
	targetURL          string
	recordingConnector *recordingGatewayConnector
	httpCapability     server.ClientCapability
	gatewayProxyDonID  string
}

func setupMultiDonRoutingEnv(
	ctx context.Context,
	t *testing.T,
	lggr logger.Logger,
	gatewayProxyDonID string,
	usNodeURL string,
	euNodeURL string,
) multiDonRoutingEnv {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/routed", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, "routed"); err != nil {
			t.Errorf("failed to write response: %v", err)
		}
	})

	listener, cleanup := startTestHTTPServer(t, mux)
	t.Cleanup(cleanup)

	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	publicKey := strings.ToLower(crypto.PubkeyToAddress(privateKey.PublicKey).Hex())

	netClient := newTestNetworkClient(t, listener.Addr().(*net.TCPAddr), lggr)
	gatewayUS := newTestGatewayFromConfig(t, multiDonGatewayConfig("gateway_us", publicKey), netClient, lggr)
	gatewayEU := newTestGatewayFromConfig(t, multiDonGatewayConfig("gateway_eu", publicKey), netClient, lggr)

	if usNodeURL == "" {
		usNodeURL = fmt.Sprintf("ws://localhost:%d/node", gatewayUS.GetNodePort())
	}
	if euNodeURL == "" {
		euNodeURL = fmt.Sprintf("ws://localhost:%d/node", gatewayEU.GetNodePort())
	}

	nodeConfig := fmt.Sprintf(multiDonNodeConfigTemplate, publicKey, usNodeURL, euNodeURL)
	var cfg connector.ConnectorConfig
	require.NoError(t, toml.Unmarshal([]byte(nodeConfig), &cfg))

	gc, err := connector.NewGatewayConnector(&cfg, &client{privateKey: privateKey}, clockwork.NewRealClock(), lggr)
	require.NoError(t, err)
	servicetest.Run(t, gc)

	recordingConnector := &recordingGatewayConnector{GatewayConnector: gc}

	settingsGetter, err := settings.NewJSONGetter([]byte(fmt.Sprintf(`{
		"org": {
			%q: {
				"PerWorkflow": {
					"HTTPAction": {
						"GatewayProxyDonID": %q
					}
				}
			}
		}
	}`, multiDonTestOrgID, gatewayProxyDonID)))
	require.NoError(t, err)

	httpCapability := httpcap.NewService(lggr, limits.Factory{
		Logger:   lggr,
		Meter:    beholder.GetMeter(),
		Settings: settingsGetter,
	})
	err = httpCapability.Initialise(ctx, core.StandardCapabilitiesDependencies{
		Config:           serviceConfigTemplate,
		GatewayConnector: recordingConnector,
	})
	require.NoError(t, err)

	return multiDonRoutingEnv{
		targetURL:          fmt.Sprintf("http://%s/routed", listener.Addr().String()),
		recordingConnector: recordingConnector,
		httpCapability:     httpCapability,
		gatewayProxyDonID:  gatewayProxyDonID,
	}
}

func multiDonRequestMetadata() capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		OrgID:               multiDonTestOrgID,
		WorkflowOwner:       fmt.Sprintf("owner_%s", uuid.New().String()),
		WorkflowID:          fmt.Sprintf("workflow_%s", uuid.New().String()),
		WorkflowExecutionID: fmt.Sprintf("execution_%s", uuid.New().String()),
	}
}

func assertRoutedThroughResolvedDon(ctx context.Context, t *testing.T, env multiDonRoutingEnv, wantGatewayPrefix string) {
	t.Helper()

	output, err := env.httpCapability.SendRequest(ctx, multiDonRequestMetadata(), &httpclient.Request{
		Url:    env.targetURL,
		Method: "GET",
	})
	require.NoError(t, err)
	require.NotNil(t, output)
	require.Equal(t, uint32(http.StatusOK), output.Response.StatusCode)
	require.Equal(t, "routed", string(output.Response.Body))

	require.NotEmpty(t, env.recordingConnector.lastGatewayID)
	require.True(t, strings.HasPrefix(env.recordingConnector.lastGatewayID, wantGatewayPrefix))

	multiGC, ok := env.recordingConnector.GatewayConnector.(core.MultiGatewayConnector)
	require.True(t, ok)
	gatewayDonID, donErr := multiGC.DonIDForGateway(ctx, env.recordingConnector.lastGatewayID)
	require.NoError(t, donErr)
	require.Equal(t, env.gatewayProxyDonID, gatewayDonID)
}

func TestHTTPActionCapability_multiDonGatewayRouting(t *testing.T) {
	ctx := t.Context()
	lggr := logger.Test(t)

	t.Run("routes through US gateway when GatewayProxyDonID resolves to gateway_don_us", func(t *testing.T) {
		env := setupMultiDonRoutingEnv(ctx, t, lggr, gatewayDonUS, "", "")
		assertRoutedThroughResolvedDon(ctx, t, env, "gateway_us")
	})

	t.Run("routes through EU gateway when GatewayProxyDonID resolves to gateway_don_eu", func(t *testing.T) {
		env := setupMultiDonRoutingEnv(ctx, t, lggr, gatewayDonEU, "", "")
		assertRoutedThroughResolvedDon(ctx, t, env, "gateway_eu")
	})

	t.Run("does not route through other DON gateway when only matching gateway is reachable", func(t *testing.T) {
		// setup so that only EU DON is reachable
		env := setupMultiDonRoutingEnv(ctx, t, lggr, gatewayDonEU, "ws://127.0.0.1:1/node", "")
		assertRoutedThroughResolvedDon(ctx, t, env, "gateway_eu")

		// Create new env with unreachable US DON -- expect failure
		envUS := setupMultiDonRoutingEnv(ctx, t, lggr, gatewayDonUS, "ws://127.0.0.1:1/node", "")
		_, err := envUS.httpCapability.SendRequest(ctx, multiDonRequestMetadata(), &httpclient.Request{
			Url:    envUS.targetURL,
			Method: "GET",
		})
		require.Error(t, err)

		var capErr caperrors.Error
		require.ErrorAs(t, err, &capErr)
		require.Empty(t, envUS.recordingConnector.lastGatewayID)
	})
}
