package http_test

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"

	triggercap "github.com/smartcontractkit/capabilities/http_trigger/trigger"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const triggerGatewayConfigTemplate = `
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
      "DonId": "workflows",
      "HandlerName": "http-capabilities",
      "HandlerConfig": {
        "MaxAllowedMessageAgeSec": 1000,
        "NodeRateLimiter": {
          "GlobalBurst": 10,
          "GlobalRPS": 50,
          "PerSenderBurst": 10,
          "PerSenderRPS": 10
        },
		"UserRateLimiter": {
		  "GlobalBurst": 10,
          "GlobalRPS": 50,
          "PerSenderBurst": 10,
          "PerSenderRPS": 10	
		}
      },
      "Members": [
        {
          "Address": "%s",
          "Name": "test_node_1"
        },
		{
          "Address": "%s",
          "Name": "test_node_2"
        },
		{
          "Address": "%s",
          "Name": "test_node_3"
        },
		{
          "Address": "%s",
          "Name": "test_node_4"
        }
      ]
    }
  ]
}
`

const triggerServiceConfigTemplate = `
{
	"incomingRateLimiter": {
		"globalBurst": 10,
		"globalRPS": 50,
		"perSenderBurst": 10,
		"perSenderRPS": 10
	},
	"outgoingRateLimiter": {
		"globalBurst": 10,
		"globalRPS": 50,
		"perSenderBurst": 10,
		"perSenderRPS": 10
	}
}
`

func nodeKeys(t *testing.T, numNodes int) []*ecdsa.PrivateKey {
	var keys []*ecdsa.PrivateKey
	for i := 0; i < numNodes; i++ {
		privateKey, err := crypto.GenerateKey()
		require.NoError(t, err)
		keys = append(keys, privateKey)
	}
	return keys
}

func TestHTTPTrigger(t *testing.T) {
	ctx := t.Context()
	lggr := logger.Test(t)

	f := 1
	numNodes := 3*f + 1
	nodeKeys := nodeKeys(t, numNodes)

	gatewayConfigStr := fmt.Sprintf(
		triggerGatewayConfigTemplate,
		strings.ToLower(crypto.PubkeyToAddress(nodeKeys[0].PublicKey).Hex()),
		strings.ToLower(crypto.PubkeyToAddress(nodeKeys[1].PublicKey).Hex()),
		strings.ToLower(crypto.PubkeyToAddress(nodeKeys[2].PublicKey).Hex()),
		strings.ToLower(crypto.PubkeyToAddress(nodeKeys[3].PublicKey).Hex()),
	)
	gateway := newTestGateway(t, gatewayConfigStr, nil, lggr)
	nodeURL := fmt.Sprintf("ws://localhost:%d/node", gateway.GetNodePort())
	userURL := fmt.Sprintf("http://localhost:%d/user", gateway.GetUserPort())

	var triggerCaps []server.HTTPCapability
	for i := 0; i < numNodes; i++ {
		triggerCaps = append(triggerCaps, newTriggerHTTPCapability(ctx, t, nodeURL, nodeKeys[i], lggr))
	}

	payload := `{
		"jsonrpc": "2.0",
		"id": "8f73d3a4-6d7c-4d1d-b9f2-28c8f630fa27",
		"method": "workflows.execute",
		"params": {
			"workflow": {
				"workflowID": "0xe3c0f8139e9e4cf0b2c31c70f3f4ae12"
			},
			"input": {"key": "value", "count": 5}
		}
	}`

	time.Sleep(2 * time.Second) // Allow time for the capabilities to initialize

	resp, err := http.Post(userURL, "application/json", strings.NewReader(payload))
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	t.Logf("Received response: %s", string(body))
}

func newTriggerHTTPCapability(ctx context.Context, t *testing.T, nodeURL string, privateKey *ecdsa.PrivateKey, lggr logger.Logger) server.HTTPCapability {
	gc := newTestGatewayConnector(t, "workflows", nodeURL, privateKey, lggr)
	triggerCap := triggercap.NewService(lggr)
	err := triggerCap.Initialise(ctx, triggerServiceConfigTemplate, nil, nil, nil, nil, nil, nil, gc, nil)
	require.NoError(t, err)
	err = triggerCap.Start(ctx)
	require.NoError(t, err)
	return triggerCap
}
