package http_test

import (
	"context"
	"crypto/ecdsa"
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

	triggercap "github.com/smartcontractkit/capabilities/http_trigger/trigger"

	"github.com/smartcontractkit/chainlink/v2/core/services/gateway"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	triggersdk "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http/server"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink/v2/core/utils"
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
	  "F": 1,
      "HandlerConfig": {
		"AuthAggregationIntervalMs": 100,
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
		%s
      ]
    }
  ]
}
`

const memberTemplate = `{
	"Address": "%s",
    "Name": "test_node_%d"
}`

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

type testEnv struct {
	ctx              context.Context
	lggr             logger.Logger
	numNodes         int
	nodeKeys         []*ecdsa.PrivateKey
	signingKey       *ecdsa.PrivateKey
	gateway          gateway.Gateway
	nodeURL          string
	userURL          string
	triggerCaps      []server.HTTPCapability
	triggerChs       []<-chan capabilities.TriggerAndId[*triggersdk.Payload]
	workflowSelector gateway_common.WorkflowSelector
}

func setupTestEnv(t *testing.T, numNodes int) *testEnv {
	ctx := t.Context()
	lggr := logger.Test(t)
	nodeKeys := nodeKeys(t, numNodes)
	signingKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	workflowID := "0xe3c0f8139e9e4cf0b2c31c70f3f4ae12"
	workflowOwner := "0x5966259d342dFAdf20B077DD2Ad4920B8bC0895D"
	workflowName := "testWorkflow"
	workflowTag := "testTag"
	workflowSelector := gateway_common.WorkflowSelector{
		WorkflowID:    workflowID,
		WorkflowOwner: workflowOwner,
		WorkflowName:  workflowName,
		WorkflowTag:   workflowTag,
	}

	membersStr := make([]string, 0, numNodes)
	for i, key := range nodeKeys {
		membersStr = append(membersStr, fmt.Sprintf(memberTemplate, crypto.PubkeyToAddress(key.PublicKey).Hex(), i))
	}

	gatewayConfigStr := fmt.Sprintf(
		triggerGatewayConfigTemplate,
		strings.Join(membersStr, ","),
	)
	gateway := newTestGateway(t, gatewayConfigStr, nil, lggr)
	nodeURL := fmt.Sprintf("ws://localhost:%d/node", gateway.GetNodePort())
	userURL := fmt.Sprintf("http://localhost:%d/user", gateway.GetUserPort())

	var triggerCaps []server.HTTPCapability
	var triggerChs []<-chan capabilities.TriggerAndId[*triggersdk.Payload]
	for i := 0; i < numNodes; i++ {
		triggerCap, ch := newTriggerHTTPCapability(ctx, t, nodeURL, nodeKeys[i], signingKey, lggr, workflowSelector)
		triggerCaps = append(triggerCaps, triggerCap)
		triggerChs = append(triggerChs, ch)
	}
	return &testEnv{
		ctx:              ctx,
		lggr:             lggr,
		numNodes:         numNodes,
		nodeKeys:         nodeKeys,
		signingKey:       signingKey,
		gateway:          gateway,
		nodeURL:          nodeURL,
		userURL:          userURL,
		triggerCaps:      triggerCaps,
		triggerChs:       triggerChs,
		workflowSelector: workflowSelector,
	}
}

func sampleRequest(ctx context.Context, t *testing.T, env *testEnv) (*http.Request, string, map[string]any) {
	input := make(map[string]any)
	input["key"] = "value"
	input["count"] = 5.0
	return createRequest(ctx, t, env, uuid.New().String(), input)
}

func createRequest(ctx context.Context, t *testing.T, env *testEnv, requestID string, input map[string]any) (*http.Request, string, map[string]any) {
	marshalledInput, err := json.Marshal(input)
	require.NoError(t, err)
	rawInput := json.RawMessage(marshalledInput)
	req := jsonrpc.Request[gateway_common.HTTPTriggerRequest]{
		Version: jsonrpc.JsonRpcVersion,
		ID:      requestID,
		Method:  "workflows.execute",
		Params: &gateway_common.HTTPTriggerRequest{
			Workflow: gateway_common.WorkflowSelector{
				WorkflowID: env.workflowSelector.WorkflowID,
			},
			Input: rawInput,
		},
	}
	issuer := "0x" + crypto.PubkeyToAddress(env.signingKey.PublicKey).Hex()
	token, err := utils.CreateRequestJWT(req, utils.WithIssuer(issuer))
	require.NoError(t, err)
	tokenString, err := token.SignedString(env.signingKey)
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(req)
	require.NoError(t, err)
	payload := string(payloadBytes)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, env.userURL, strings.NewReader(payload))
	require.NoError(t, err)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+tokenString)
	return httpReq, requestID, input
}

func makeRequestAndValidateResponse(t *testing.T, req *http.Request, expectedRequestID string, expectedWorkflowID string) *jsonrpc.Response[gateway_common.HTTPTriggerResponse] {
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var respBody jsonrpc.Response[gateway_common.HTTPTriggerResponse]
	err = json.Unmarshal(body, &respBody)
	require.NoError(t, err)

	require.Equal(t, expectedRequestID, respBody.ID)
	require.Equal(t, gateway_common.HTTPTriggerStatusAccepted, respBody.Result.Status)

	executionID, err := workflows.EncodeExecutionID(expectedWorkflowID, expectedRequestID)
	require.NoError(t, err)
	require.Equal(t, executionID, respBody.Result.WorkflowExecutionID)
	require.Equal(t, expectedWorkflowID, respBody.Result.WorkflowID)

	return &respBody
}

func validateTriggersReceived(t *testing.T, env *testEnv, expectedExecutionID string, expectedInput map[string]any) {
	for i, ch := range env.triggerChs {
		select {
		case payload := <-ch:
			require.NotNil(t, payload)
			require.Equal(t, expectedExecutionID, payload.Id)
			require.EqualValues(t, expectedInput, payload.Trigger.Input.AsMap())
			require.Equal(t, triggersdk.KeyType_KEY_TYPE_ECDSA, payload.Trigger.Key.Type)
			require.Equal(t, strings.ToLower(crypto.PubkeyToAddress(env.signingKey.PublicKey).Hex()), payload.Trigger.Key.PublicKey)
		default:
			t.Fatalf("Node %d did not receive a trigger in time", i)
		}
	}
}

func validateNoTriggersReceived(t *testing.T, env *testEnv) {
	for i, ch := range env.triggerChs {
		select {
		case payload := <-ch:
			t.Fatalf("Node %d unexpectedly received a trigger: %+v", i, payload)
			//TODO: REMOVE TIME
		case <-time.After(500 * time.Millisecond):
			// Expected - no trigger should be received
		}
	}
}

func TestHTTPTrigger(t *testing.T) {
	f := 1
	numNodes := 3*f + 1
	env := setupTestEnv(t, numNodes)
	req, requestID, input := sampleRequest(t.Context(), t, env)
	makeRequestAndValidateResponse(t, req, requestID, env.workflowSelector.WorkflowID)
	executionID, err := workflows.EncodeExecutionID(env.workflowSelector.WorkflowID, requestID)
	require.NoError(t, err)
	validateTriggersReceived(t, env, executionID, input)
}

func TestHTTPTrigger_Idempotency(t *testing.T) {
	f := 1
	numNodes := 3*f + 1
	env := setupTestEnv(t, numNodes)
	requestID := uuid.New().String()
	initialInput := map[string]any{
		"key":   "value",
		"count": 10.0,
	}
	req1, _, input1 := createRequest(t.Context(), t, env, requestID, initialInput)
	makeRequestAndValidateResponse(t, req1, requestID, env.workflowSelector.WorkflowID)
	executionID, err := workflows.EncodeExecutionID(env.workflowSelector.WorkflowID, requestID)
	require.NoError(t, err)
	validateTriggersReceived(t, env, executionID, input1)

	// Step 2: Make the same request again (idempotent - should return cached response, no new trigger)
	req2, _, _ := createRequest(t.Context(), t, env, requestID, initialInput)
	makeRequestAndValidateResponse(t, req2, requestID, env.workflowSelector.WorkflowID)
	validateNoTriggersReceived(t, env)

	// Step 3: Make a request with the same ID but different payload (should error)
	conflictingInput := map[string]any{
		"key":   "different_value",
		"count": 20.0,
	}
	req3, _, _ := createRequest(t.Context(), t, env, requestID, conflictingInput)
	resp, err := http.DefaultClient.Do(req3)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	var respBody jsonrpc.Response[json.RawMessage]
	err = json.Unmarshal(body, &respBody)
	require.NoError(t, err)
	require.Equal(t, requestID, respBody.ID)
	require.Empty(t, respBody.Result)
	require.Equal(t, jsonrpc.ErrConflict, respBody.Error.Code)
	require.Equal(t, "Request already in progress with different payload", respBody.Error.Message)
	validateNoTriggersReceived(t, env)
}

func TestHTTPTrigger_InsufficientNodes(t *testing.T) {
	env := setupTestEnv(t, 2) // 3F+1 nodes = 4, but only 2 nodes
	ctx, cancel := context.WithTimeout(env.ctx, 5*time.Second)
	defer cancel()
	req, requestID, input := sampleRequest(ctx, t, env)
	executionID, err := workflows.EncodeExecutionID(env.workflowSelector.WorkflowID, requestID)
	require.NoError(t, err)
	_, err = http.DefaultClient.Do(req)
	// Error is expected because insufficient nodes for 2f + 1 response
	require.Error(t, err)
	for i, ch := range env.triggerChs {
		select {
		case payload := <-ch:
			require.NotNil(t, payload)
			require.Equal(t, executionID, payload.Id)
			require.EqualValues(t, input, payload.Trigger.Input.AsMap())
			require.Equal(t, triggersdk.KeyType_KEY_TYPE_ECDSA, payload.Trigger.Key.Type)
			require.Equal(t, strings.ToLower(crypto.PubkeyToAddress(env.signingKey.PublicKey).Hex()), payload.Trigger.Key.PublicKey)
		default:
			t.Fatalf("Node %d did not receive a trigger in time", i)
		}
	}
}

func newTriggerHTTPCapability(ctx context.Context, t *testing.T, nodeURL string, privateKey *ecdsa.PrivateKey, signingKey *ecdsa.PrivateKey, lggr logger.Logger, workflowSelector gateway_common.WorkflowSelector) (server.HTTPCapability, <-chan capabilities.TriggerAndId[*triggersdk.Payload]) {
	gc := newTestGatewayConnector(t, "workflows", nodeURL, privateKey, lggr)
	triggerCap := triggercap.NewService(lggr)
	err := triggerCap.Initialise(ctx, triggerServiceConfigTemplate, nil, nil, nil, nil, nil, nil, gc, nil)
	require.NoError(t, err)
	err = triggerCap.Start(ctx)
	require.NoError(t, err)
	requestMetadata := capabilities.RequestMetadata{
		WorkflowID:    workflowSelector.WorkflowID,
		WorkflowOwner: workflowSelector.WorkflowOwner,
		WorkflowName:  workflowSelector.WorkflowName,
		WorkflowTag:   workflowSelector.WorkflowTag,
	}
	ch, err := triggerCap.RegisterTrigger(ctx, "trigger-id", requestMetadata, &triggersdk.Config{
		AuthorizedKeys: []*triggersdk.AuthorizedKey{
			{
				PublicKey: crypto.PubkeyToAddress(signingKey.PublicKey).Hex(),
				Type:      triggersdk.KeyType_KEY_TYPE_ECDSA,
			},
		},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return triggerCap.Ready() == nil
	}, 30*time.Second, 100*time.Millisecond)
	return triggerCap, ch
}
