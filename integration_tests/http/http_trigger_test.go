package http

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	triggercap "github.com/smartcontractkit/capabilities/http_trigger/trigger"

	"github.com/smartcontractkit/chainlink/v2/core/services/gateway"
	"github.com/smartcontractkit/chainlink/v2/core/utils"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	triggersdk "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
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
      "DonId": "test_don",
      "F": 1,
      "Handlers": [
		{
			"Name": "http-capabilities",
			"ServiceName": "workflows",
			"Config": {
				"MaxTriggerRequestDurationMs": 5000,
				"MetadataPullIntervalMs": 1000,
				"MetadataAggregationIntervalMs": 1000,
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
			}
	    }
	  ],
      "Members": [ %s ]
    }
  ]
}
`

const memberTemplate = `{
	"Address": "%s",
    "Name": "test_node_%d"
}`

const workflowID = "0x217ca1cb7b52136b3baedb2a13e4609fa86439b87a1bc48fea6d95f19444cf72"

// Workflow reference constants for testing
const (
	workflowOwner = "0x1234567890123456789012345678901234567890"
	workflowName  = "test-workflow"
	workflowTag   = "production"
)

func nodeKeys(t *testing.T, numNodes int) []*ecdsa.PrivateKey {
	var keys []*ecdsa.PrivateKey
	for range numNodes {
		privateKey, err := crypto.GenerateKey()
		require.NoError(t, err)
		keys = append(keys, privateKey)
	}
	return keys
}

type testEnv struct {
	lggr        logger.Logger
	numNodes    int
	nodeKeys    []*ecdsa.PrivateKey
	signingKey  *ecdsa.PrivateKey
	gateway     gateway.Gateway
	nodeURL     string
	userURL     string
	triggerCaps []server.HTTPCapability
	triggerChs  []<-chan capabilities.TriggerAndId[*triggersdk.Payload]
}

func setupTestEnv(t *testing.T, numNodes int) *testEnv {
	ctx := t.Context()
	lggr := logger.Test(t)
	nodeKeys := nodeKeys(t, numNodes)
	signingKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	membersStr := make([]string, 0, numNodes)
	for i, key := range nodeKeys {
		membersStr = append(membersStr, fmt.Sprintf(memberTemplate, crypto.PubkeyToAddress(key.PublicKey).Hex(), i))
	}

	gatewayConfigStr := fmt.Sprintf(
		triggerGatewayConfigTemplate,
		strings.Join(membersStr, ","),
	)
	gateway := newTestGatewayFromConfig(t, gatewayConfigStr, nil, lggr)
	nodeURL := fmt.Sprintf("ws://localhost:%d/node", gateway.GetNodePort())
	userURL := fmt.Sprintf("http://localhost:%d/user", gateway.GetUserPort())

	var triggerCaps []server.HTTPCapability
	var triggerChs []<-chan capabilities.TriggerAndId[*triggersdk.Payload]
	for i := range numNodes {
		triggerCap, ch := newTriggerHTTPCapability(ctx, t, nodeURL, nodeKeys[i], signingKey, lggr)
		triggerCaps = append(triggerCaps, triggerCap)
		triggerChs = append(triggerChs, ch)
	}
	return &testEnv{
		lggr:        lggr,
		numNodes:    numNodes,
		nodeKeys:    nodeKeys,
		signingKey:  signingKey,
		gateway:     gateway,
		nodeURL:     nodeURL,
		userURL:     userURL,
		triggerCaps: triggerCaps,
		triggerChs:  triggerChs,
	}
}

func createSampleRequest(t *testing.T, url string, key *ecdsa.PrivateKey, workflow gateway_common.WorkflowSelector, requestID string) (*http.Request, string, map[string]any) {
	input := make(map[string]any)
	input["key"] = "value"
	input["count"] = 5.0
	marshalledInput, err := json.Marshal(input)
	require.NoError(t, err)
	rawInput := json.RawMessage(marshalledInput)

	req := jsonrpc.Request[gateway_common.HTTPTriggerRequest]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      requestID,
		Method:  gateway_common.MethodWorkflowExecute,
		Params: &gateway_common.HTTPTriggerRequest{
			Workflow: workflow,
			Input:    rawInput,
		},
	}
	payloadBytes, err := json.Marshal(req)
	require.NoError(t, err)
	payload := string(payloadBytes)
	unsignedToken, err := utils.CreateRequestJWT(req)
	require.NoError(t, err)
	signedToken, err := unsignedToken.SignedString(key)
	require.NoError(t, err)
	httpReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(payload))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", signedToken))
	return httpReq, requestID, input
}

func sampleRequest(t *testing.T, url string, key *ecdsa.PrivateKey) (*http.Request, string, map[string]any) {
	workflow := gateway_common.WorkflowSelector{
		WorkflowID: workflowID,
	}
	return createSampleRequest(t, url, key, workflow, uuid.New().String())
}

func sampleRequestWithReference(t *testing.T, url string, key *ecdsa.PrivateKey) (*http.Request, string, map[string]any) {
	workflow := gateway_common.WorkflowSelector{
		WorkflowOwner: workflowOwner,
		WorkflowName:  workflowName,
		WorkflowTag:   workflowTag,
	}
	return createSampleRequest(t, url, key, workflow, uuid.New().String())
}

func sampleRequestWithoutPrefix(t *testing.T, url string, key *ecdsa.PrivateKey) (*http.Request, string, map[string]any) {
	// Strip 0x prefix from workflowID to test normalization
	workflowIDWithoutPrefix := strings.TrimPrefix(workflowID, "0x")
	workflow := gateway_common.WorkflowSelector{
		WorkflowID: workflowIDWithoutPrefix,
	}
	return createSampleRequest(t, url, key, workflow, uuid.New().String())
}

func sampleRequestWithReferenceWithoutPrefix(t *testing.T, url string, key *ecdsa.PrivateKey) (*http.Request, string, map[string]any) {
	// Strip 0x prefix from workflowOwner to test normalization
	workflowOwnerWithoutPrefix := strings.TrimPrefix(workflowOwner, "0x")
	workflow := gateway_common.WorkflowSelector{
		WorkflowOwner: workflowOwnerWithoutPrefix,
		WorkflowName:  workflowName,
		WorkflowTag:   workflowTag,
	}
	return createSampleRequest(t, url, key, workflow, uuid.New().String())
}

func TestHTTPTrigger(t *testing.T) {
	t.Run("WithWorkflowID", func(t *testing.T) {
		testHTTPTriggerWithWorkflowID(t)
	})

	t.Run("WithWorkflowReference", func(t *testing.T) {
		testHTTPTriggerWithWorkflowReference(t)
	})

	t.Run("WithWorkflowIDWithoutPrefix", func(t *testing.T) {
		testHTTPTriggerWithWorkflowIDWithoutPrefix(t)
	})

	t.Run("WithWorkflowReferenceWithoutPrefix", func(t *testing.T) {
		testHTTPTriggerWithWorkflowReferenceWithoutPrefix(t)
	})

	t.Run("RequestDeduplication", func(t *testing.T) {
		testHTTPTriggerRequestDeduplication(t)
	})
}

func TestHTTPTrigger_InsufficientNodes(t *testing.T) {
	env := setupTestEnv(t, 2) // 3 nodes required for successful workflow execution, but only 2 nodes
	var requestID string
	var req *http.Request
	var input map[string]any
	req, requestID, input = sampleRequest(t, env.userURL, env.signingKey)
	require.Eventually(t, func() bool {
		_, err := http.DefaultClient.Do(req)
		return err != nil // request times out and returns an error if threshold of node responses is not met
	}, 30*time.Second, time.Second)
	assertTriggerPayload(t, env, requestID, input) // workflows are still triggered even if not all nodes are available
}

// requestGeneratorFunc is a function type for generating test requests
type requestGeneratorFunc func(t *testing.T, url string, key *ecdsa.PrivateKey) (*http.Request, string, map[string]any)

// runHTTPTriggerTest is a helper function that runs a standard HTTP trigger test with the given request generator
func runHTTPTriggerTest(t *testing.T, reqGen requestGeneratorFunc) {
	f := 1
	numNodes := 3*f + 1
	env := setupTestEnv(t, numNodes)
	var req *http.Request
	var requestID string
	var input map[string]any
	var body []byte
	require.Eventually(t, func() bool {
		req, requestID, input = reqGen(t, env.userURL, env.signingKey)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, time.Second)

	validateHTTPTriggerResponse(t, body, requestID, workflowID)
	assertTriggerPayload(t, env, requestID, input)
}

func testHTTPTriggerWithWorkflowID(t *testing.T) {
	runHTTPTriggerTest(t, sampleRequest)
}

func testHTTPTriggerWithWorkflowReference(t *testing.T) {
	runHTTPTriggerTest(t, sampleRequestWithReference)
}

func testHTTPTriggerWithWorkflowIDWithoutPrefix(t *testing.T) {
	runHTTPTriggerTest(t, sampleRequestWithoutPrefix)
}

func testHTTPTriggerWithWorkflowReferenceWithoutPrefix(t *testing.T) {
	runHTTPTriggerTest(t, sampleRequestWithReferenceWithoutPrefix)
}

func testHTTPTriggerRequestDeduplication(t *testing.T) {
	f := 1
	numNodes := 3*f + 1
	env := setupTestEnv(t, numNodes)

	requestID := uuid.New().String()

	workflow := gateway_common.WorkflowSelector{
		WorkflowID: workflowID,
	}

	// Make the first request
	request, _, input := createSampleRequest(t, env.userURL, env.signingKey, workflow, requestID)

	var body []byte
	require.Eventually(t, func() bool {
		resp, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, time.Second)

	validateHTTPTriggerResponse(t, body, requestID, workflowID)
	assertTriggerPayload(t, env, requestID, input)

	request, _, _ = createSampleRequest(t, env.userURL, env.signingKey, workflow, requestID)
	resp, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	validateHTTPTriggerResponse(t, body, requestID, workflowID)

	// This request should be deduplicated, so no new triggers should be sent to the nodes
	for i, ch := range env.triggerChs {
		require.Equal(t, 0, len(ch), "Node %d should not have received any new trigger payloads due to deduplication", i)
	}
}

// validateHTTPTriggerResponse validates the HTTP response and returns the execution ID for further assertions
func validateHTTPTriggerResponse(t *testing.T, body []byte, requestID string, expectedWorkflowID string) {
	var respBody jsonrpc.Response[gateway_common.HTTPTriggerResponse]
	err := json.Unmarshal(body, &respBody)
	require.NoError(t, err)
	require.Equal(t, requestID, respBody.ID)
	require.Equal(t, gateway_common.HTTPTriggerStatusAccepted, respBody.Result.Status)

	require.NotEmpty(t, respBody.Result.WorkflowID)
	workflowIDFromResponse := respBody.Result.WorkflowID
	require.Equal(t, expectedWorkflowID, workflowIDFromResponse)

	executionID, err := workflows.EncodeExecutionID(strings.TrimPrefix(workflowIDFromResponse, "0x"), requestID)
	require.NoError(t, err)
	require.Equal(t, "0x"+executionID, respBody.Result.WorkflowExecutionID)
}

func assertTriggerPayload(t *testing.T, env *testEnv, requestID string, expectedInput map[string]any) {
	for i, ch := range env.triggerChs {
		select {
		case payload := <-ch:
			require.NotNil(t, payload)
			require.Equal(t, requestID, payload.Id)
			var actualInput map[string]any
			err := json.Unmarshal(payload.Trigger.Input, &actualInput)
			require.NoError(t, err)
			require.Equal(t, expectedInput, actualInput)
			require.Equal(t, triggersdk.KeyType_KEY_TYPE_ECDSA_EVM, payload.Trigger.Key.Type)
			publicKey := strings.ToLower(crypto.PubkeyToAddress(env.signingKey.PublicKey).Hex())
			require.Equal(t, publicKey, payload.Trigger.Key.PublicKey)
		case <-time.After(1 * time.Minute):
			t.Fatalf("Node %d did not receive a trigger within 1 minute", i)
		}
	}
}

func newTriggerHTTPCapability(ctx context.Context, t *testing.T, nodeURL string, privateKey *ecdsa.PrivateKey, signingKey *ecdsa.PrivateKey, lggr logger.Logger) (server.HTTPCapability, <-chan capabilities.TriggerAndId[*triggersdk.Payload]) {
	publicKey := strings.ToLower(crypto.PubkeyToAddress(privateKey.PublicKey).Hex())
	client := &client{privateKey: privateKey}
	gc := newTestGatewayConnector(t, publicKey, nodeURL, client, lggr)
	triggerCap := triggercap.NewService(lggr, limits.Factory{Logger: lggr})
	kvStore := newTestKeyValueStore()
	err := triggerCap.Initialise(ctx, core.StandardCapabilitiesDependencies{
		Config:           "",
		Store:            kvStore,
		GatewayConnector: gc,
	})
	require.NoError(t, err)
	ch, err := triggerCap.RegisterTrigger(ctx, "trigger-id", capabilities.RequestMetadata{
		WorkflowID:    workflowID,
		WorkflowName:  hex.EncodeToString([]byte(workflows.HashTruncateName(workflowName))),
		WorkflowOwner: workflowOwner,
		WorkflowTag:   workflowTag,
	}, &triggersdk.Config{
		AuthorizedKeys: []*triggersdk.AuthorizedKey{
			{
				PublicKey: strings.ToLower(crypto.PubkeyToAddress(signingKey.PublicKey).Hex()),
				Type:      triggersdk.KeyType_KEY_TYPE_ECDSA_EVM,
			},
		},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return triggerCap.Ready() == nil
	}, 30*time.Second, time.Second)
	return triggerCap, ch
}
