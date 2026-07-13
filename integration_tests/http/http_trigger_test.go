package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/rand"
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

// Workflow reference constants for testing
const (
	workflowOwner = "0x1234567890123456789012345678901234567890"
	workflowName  = "test-workflow"
	workflowTag   = "production"
)

// generateWorkflowID generates a random 32-byte hex workflow ID with 0x prefix
func generateWorkflowID(t *testing.T) string {
	bytes := make([]byte, 32)
	_, err := rand.Read(bytes)
	require.NoError(t, err)
	return "0x" + hex.EncodeToString(bytes)
}

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
	lggr           logger.Logger
	numNodes       int
	numFaultyNodes int
	nodeKeys       []*ecdsa.PrivateKey
	signingKey     *ecdsa.PrivateKey
	gateway        gateway.Gateway
	nodeURL        string
	userURL        string
	workflowID     string
	triggerCaps    []server.HTTPCapability
	triggerChs     []<-chan capabilities.TriggerAndId[*triggersdk.Payload]
}

func setupTestEnv(t *testing.T, numHonestNodes int, numFaultyNodes int) *testEnv {
	ctx := t.Context()
	lggr := logger.Test(t)
	nodeKeys := nodeKeys(t, numHonestNodes+numFaultyNodes)
	signingKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	nodes := make([]gatewayNode, len(nodeKeys))
	for i, key := range nodeKeys {
		nodes[i] = gatewayNode{
			Name:    fmt.Sprintf("test_node_%d", i),
			Address: crypto.PubkeyToAddress(key.PublicKey).Hex(),
		}
	}

	gatewayConfigStr := buildHTTPTriggerGatewayConfig("test_gateway", 1, nodes)
	gateway := newTestGatewayFromConfig(t, gatewayConfigStr, nil, lggr)
	nodeURL := fmt.Sprintf("ws://localhost:%d/node", gateway.GetNodePort())
	userURL := fmt.Sprintf("http://localhost:%d/user", gateway.GetUserPort())
	workflowID := generateWorkflowID(t)

	var triggerCaps []server.HTTPCapability
	var triggerChs []<-chan capabilities.TriggerAndId[*triggersdk.Payload]
	for i := range numHonestNodes {
		triggerCap, ch := newTriggerHTTPCapability(ctx, t, nodeURL, nodeKeys[i], signingKey, workflowID, lggr)
		triggerCaps = append(triggerCaps, triggerCap)
		triggerChs = append(triggerChs, ch)
	}
	return &testEnv{
		lggr:           lggr,
		numNodes:       numHonestNodes,
		numFaultyNodes: numFaultyNodes,
		nodeKeys:       nodeKeys,
		signingKey:     signingKey,
		gateway:        gateway,
		nodeURL:        nodeURL,
		userURL:        userURL,
		workflowID:     workflowID,
		triggerCaps:    triggerCaps,
		triggerChs:     triggerChs,
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

func sampleRequest(t *testing.T, url string, key *ecdsa.PrivateKey, workflowID string) (*http.Request, string, map[string]any) {
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

func sampleRequestWithoutPrefix(t *testing.T, url string, key *ecdsa.PrivateKey, workflowID string) (*http.Request, string, map[string]any) {
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
	// 2 honest nodes and 1 faulty node
	// f + 1 = 2 is enough for workflow metadata aggregation
	// (f + n) // 2 + 1 = 3 is required for consensus
	// 2 honest nodes is not enough to reach consensus
	env := setupTestEnv(t, 2, 1)
	var requestID string
	var req *http.Request
	var input map[string]any
	require.Eventually(t, func() bool {
		req, requestID, input = sampleRequest(t, env.userURL, env.signingKey, env.workflowID)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode == http.StatusServiceUnavailable
	}, 30*time.Second, time.Second)
	assertTriggerPayload(t, env, requestID, input) // workflows are still triggered even if not all nodes are available
}

// requestGeneratorFunc is a function type that generates a request given a test environment
type requestGeneratorFunc func(t *testing.T, env *testEnv) (*http.Request, string, map[string]any)

// runHTTPTriggerTest is a helper function that runs a standard HTTP trigger test with the given request generator
func runHTTPTriggerTest(t *testing.T, reqGen requestGeneratorFunc) {
	f := 1
	numNodes := 3*f + 1
	env := setupTestEnv(t, numNodes, 0)
	var req *http.Request
	var requestID string
	var input map[string]any
	var body []byte
	require.Eventually(t, func() bool {
		req, requestID, input = reqGen(t, env)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode == http.StatusOK
	}, 30*time.Second, time.Second)

	validateHTTPTriggerResponse(t, body, requestID, env.workflowID)
	assertTriggerPayload(t, env, requestID, input)
}

func testHTTPTriggerWithWorkflowID(t *testing.T) {
	runHTTPTriggerTest(t, func(t *testing.T, env *testEnv) (*http.Request, string, map[string]any) {
		return sampleRequest(t, env.userURL, env.signingKey, env.workflowID)
	})
}

func testHTTPTriggerWithWorkflowReference(t *testing.T) {
	runHTTPTriggerTest(t, func(t *testing.T, env *testEnv) (*http.Request, string, map[string]any) {
		return sampleRequestWithReference(t, env.userURL, env.signingKey)
	})
}

func testHTTPTriggerWithWorkflowIDWithoutPrefix(t *testing.T) {
	runHTTPTriggerTest(t, func(t *testing.T, env *testEnv) (*http.Request, string, map[string]any) {
		return sampleRequestWithoutPrefix(t, env.userURL, env.signingKey, env.workflowID)
	})
}

func testHTTPTriggerWithWorkflowReferenceWithoutPrefix(t *testing.T) {
	runHTTPTriggerTest(t, func(t *testing.T, env *testEnv) (*http.Request, string, map[string]any) {
		return sampleRequestWithReferenceWithoutPrefix(t, env.userURL, env.signingKey)
	})
}

func testHTTPTriggerRequestDeduplication(t *testing.T) {
	f := 1
	numNodes := 3*f + 1
	env := setupTestEnv(t, numNodes, 0)

	requestID := uuid.New().String()

	workflow := gateway_common.WorkflowSelector{
		WorkflowID: env.workflowID,
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

	validateHTTPTriggerResponse(t, body, requestID, env.workflowID)
	assertTriggerPayload(t, env, requestID, input)

	request, _, _ = createSampleRequest(t, env.userURL, env.signingKey, workflow, requestID)
	resp, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	validateHTTPTriggerResponse(t, body, requestID, env.workflowID)

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

	executionID, err := workflows.GenerateExecutionIDWithTriggerIndex(strings.TrimPrefix(workflowIDFromResponse, "0x"), requestID, 0)
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

func newTriggerHTTPCapability(ctx context.Context, t *testing.T, nodeURL string, privateKey *ecdsa.PrivateKey, signingKey *ecdsa.PrivateKey, workflowID string, lggr logger.Logger) (server.HTTPCapability, <-chan capabilities.TriggerAndId[*triggersdk.Payload]) {
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
