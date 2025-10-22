package trigger

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
)

const (
	publicKey = "0xA18B5D6DB47fB7b0974505D7aB544e24478B6e98"
)

type mockGatewayConnector struct {
	SendToGatewayCalled bool
	SendToGatewayArgs   struct {
		GatewayID string
		Msg       *jsonrpc.Response[json.RawMessage]
	}
}

func (m *mockGatewayConnector) AddHandler(ctx context.Context, methods []string, handler core.GatewayConnectorHandler) error {
	return nil
}
func (m *mockGatewayConnector) RemoveHandler(ctx context.Context, methods []string) error { return nil }
func (m *mockGatewayConnector) SendToGateway(ctx context.Context, gatewayID string, resp *jsonrpc.Response[json.RawMessage]) error {
	m.SendToGatewayCalled = true
	m.SendToGatewayArgs.GatewayID = gatewayID
	m.SendToGatewayArgs.Msg = resp
	return nil
}
func (m *mockGatewayConnector) SignMessage(ctx context.Context, msg []byte) ([]byte, error) {
	return nil, nil
}
func (m *mockGatewayConnector) GatewayIDs(ctx context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockGatewayConnector) DonID(ctx context.Context) (string, error) {
	return "", nil
}
func (m *mockGatewayConnector) AwaitConnection(ctx context.Context, gatewayID string) error {
	return nil
}

// gatewayRequest creates a test request message with the given method
func gatewayRequest(t *testing.T, method string) (*jsonrpc.Request[json.RawMessage], gateway_common.AuthorizedKey) {
	key := gateway_common.AuthorizedKey{
		KeyType:   gateway_common.KeyTypeECDSAEVM,
		PublicKey: publicKey,
	}
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowID: testWorkflowID,
		},
		Input: json.RawMessage(`{"key":"value"}`),
		Key:   key,
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	jsonPayloadMsg := json.RawMessage(jsonPayload)
	return &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  &jsonPayloadMsg,
	}, key
}

// gatewayRequestByTag creates a test request message with the given method
func gatewayRequestByTag(t *testing.T, method string, workflowOwner string) (*jsonrpc.Request[json.RawMessage], gateway_common.AuthorizedKey) {
	key := gateway_common.AuthorizedKey{
		KeyType:   gateway_common.KeyTypeECDSAEVM,
		PublicKey: publicKey,
	}
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowOwner: workflowOwner,
			WorkflowName:  "workflowName",
			WorkflowTag:   testWorkflowTag,
		},
		Input: json.RawMessage(`{"key":"value"}`),
		Key:   key,
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	jsonPayloadMsg := json.RawMessage(jsonPayload)
	return &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  &jsonPayloadMsg,
	}, key
}

// gatewayRequestWithoutPrefix creates a test request with workflowID lacking 0x prefix
func gatewayRequestWithoutPrefix(t *testing.T, method string) (*jsonrpc.Request[json.RawMessage], gateway_common.AuthorizedKey) {
	key := gateway_common.AuthorizedKey{
		KeyType:   gateway_common.KeyTypeECDSAEVM,
		PublicKey: publicKey,
	}
	// Strip 0x prefix from workflowID to test normalization
	workflowIDWithoutPrefix := strings.TrimPrefix(testWorkflowID, "0x")
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowID: workflowIDWithoutPrefix,
		},
		Input: json.RawMessage(`{"key":"value"}`),
		Key:   key,
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	jsonPayloadMsg := json.RawMessage(jsonPayload)
	return &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  &jsonPayloadMsg,
	}, key
}

// gatewayRequestByTagWithoutPrefix creates a test request with workflowOwner lacking 0x prefix
func gatewayRequestByTagWithoutPrefix(t *testing.T, method string, workflowOwner string) (*jsonrpc.Request[json.RawMessage], gateway_common.AuthorizedKey) {
	key := gateway_common.AuthorizedKey{
		KeyType:   gateway_common.KeyTypeECDSAEVM,
		PublicKey: publicKey,
	}
	// Strip 0x prefix from workflowOwner to test normalization
	workflowOwnerWithoutPrefix := strings.TrimPrefix(workflowOwner, "0x")
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowOwner: workflowOwnerWithoutPrefix,
			WorkflowName:  "workflowName",
			WorkflowTag:   testWorkflowTag,
		},
		Input: json.RawMessage(`{"key":"value"}`),
		Key:   key,
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	jsonPayloadMsg := json.RawMessage(jsonPayload)
	return &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  &jsonPayloadMsg,
	}, key
}

func rateLimiterConfig() ratelimit.RateLimiterConfig {
	return ratelimit.RateLimiterConfig{
		GlobalRPS:      100.0,
		GlobalBurst:    100,
		PerSenderRPS:   100.0,
		PerSenderBurst: 100,
	}
}

func newMetrics(t *testing.T) *Metrics {
	m, err := NewMetrics()
	require.NoError(t, err)
	return m
}

// Helper for setting up proxy and mockConnector for SendRequest tests
func setup(t *testing.T, lggr logger.Logger) (*connectorHandler, *mockGatewayConnector, <-chan capabilities.TriggerAndId[*http.Payload], *requestCache) {
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{
		MetadataBatchSize:            10,
		MaxAuthorizedKeysPerWorkflow: 3,
	}
	irl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	orl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	store := newWorkflowStore(lggr)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	metadataPublisher := NewGatewayMetadataPublisher(
		lggr,
		mockConnector,
		orl,
		store,
		cfg,
		metrics,
	)
	kvstore := newTestKVStore()
	requestCache := newRequestCache(logger.Sugared(lggr), kvstore, time.Hour)

	handler, err := NewConnectorHandler(
		lggr,
		mockConnector,
		cfg,
		orl,
		irl,
		store,
		metadataPublisher,
		requestCache,
		newMetrics(t),
		nil,
	)
	require.NoError(t, err)
	sdkCfg := &http.Config{
		AuthorizedKeys: []*http.AuthorizedKey{
			{
				PublicKey: publicKey,
				Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
			},
		},
	}
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	err = handler.RegisterWorkflow(context.Background(), WorkflowRegistrationInput{
		WorkflowSelector: gateway_common.WorkflowSelector{
			WorkflowID:    testWorkflowID,
			WorkflowOwner: testWorkflowOwner,
			WorkflowName:  testWorkflowName,
			WorkflowTag:   testWorkflowTag,
		},
		Config: sdkCfg,
		Metadata: WorkflowRegistrationMetadata{
			WorkflowRegistryChainSelector: "test-chain-selector",
			WorkflowRegistryAddress:       "test-registry-address",
			EngineVersion:                 "1.0.0",
			WorkflowDONID:                 42,
		},
	}, sendCh)
	require.NoError(t, err)

	return handler, mockConnector, sendCh, requestCache
}

func requireWorkflowTriggered(t *testing.T, triggerCh <-chan capabilities.TriggerAndId[*http.Payload], req *jsonrpc.Request[json.RawMessage], connector *mockGatewayConnector, handler *connectorHandler, key gateway_common.AuthorizedKey, requestCache *requestCache) {
	// Start a goroutine to assert that the correct trigger payload is received
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-t.Context().Done():
			t.Errorf("Test context was cancelled before trigger was received")
		case triggerReq := <-triggerCh:
			var input map[string]any
			err := json.Unmarshal(triggerReq.Trigger.Input, &input)
			require.NoError(t, err)
			require.Len(t, input, 1)
			require.Equal(t, "value", input["key"])
			require.NotNil(t, triggerReq.Trigger.Key, "Key should not be nil in trigger payload")
			require.Equal(t, http.KeyType_KEY_TYPE_ECDSA_EVM, triggerReq.Trigger.Key.Type, "Key type should match")
			require.Equal(t, key.PublicKey, triggerReq.Trigger.Key.PublicKey, "Public key should match")
		}
	}()
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)

	require.True(t, connector.SendToGatewayCalled)
	require.Equal(t, "gw1", connector.SendToGatewayArgs.GatewayID)

	resp := connector.SendToGatewayArgs.Msg
	require.Equal(t, "2.0", resp.Version)
	require.Equal(t, req.ID, resp.ID)
	require.Nil(t, resp.Error, "Response should not contain an error")

	var triggerResp gateway_common.HTTPTriggerResponse
	require.NotNil(t, resp.Result)
	err = json.Unmarshal(*resp.Result, &triggerResp)
	require.NoError(t, err)
	require.Equal(t, testWorkflowID, triggerResp.WorkflowID)

	executionID, err := workflows.EncodeExecutionID(strings.TrimPrefix(testWorkflowID, "0x"), req.ID)
	require.NoError(t, err)
	executionID = ensureHexPrefix(executionID)
	require.Equal(t, executionID, triggerResp.WorkflowExecutionID)
	select {
	case <-t.Context().Done():
		t.Errorf("Test context was cancelled before trigger was received")
	case <-done: // Ensure goroutine completes
	}

	entry, err := requestCache.get(t.Context(), req.ID)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.Equal(t, executionID, entry.ExecutionID)
	require.Equal(t, req.ID, entry.RequestID)
}

// TestHandleGatewayMessage_Success tests successful request processing
func TestHandleGatewayMessage_Success(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, requestCache := setup(t, lggr)
	req, key := gatewayRequest(t, gateway_common.MethodWorkflowExecute)
	requireWorkflowTriggered(t, triggerCh, req, connector, handler, key, requestCache)
}

// TestHandleGatewayMessage_ByTag tests successful request processing using
// workflowOwner/Name/Tag combination
func TestHandleGatewayMessage_ByTag(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, requestCache := setup(t, lggr)
	req, key := gatewayRequestByTag(t, gateway_common.MethodWorkflowExecute, testWorkflowOwner)
	requireWorkflowTriggered(t, triggerCh, req, connector, handler, key, requestCache)
}

// TestHandleGatewayMessage_WithoutPrefix tests successful request processing
// with workflowID that lacks 0x prefix (should be normalized)
func TestHandleGatewayMessage_WithoutPrefix(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, requestCache := setup(t, lggr)
	req, key := gatewayRequestWithoutPrefix(t, gateway_common.MethodWorkflowExecute)
	requireWorkflowTriggered(t, triggerCh, req, connector, handler, key, requestCache)
}

// TestHandleGatewayMessage_ByTagWithoutPrefix tests successful request processing using
// workflowOwner/Name/Tag combination where workflowOwner lacks 0x prefix (should be normalized)
func TestHandleGatewayMessage_ByTagWithoutPrefix(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, requestCache := setup(t, lggr)
	req, key := gatewayRequestByTagWithoutPrefix(t, gateway_common.MethodWorkflowExecute, testWorkflowOwner)
	requireWorkflowTriggered(t, triggerCh, req, connector, handler, key, requestCache)
}

func TestHandleGatewayMessage_ByTag_WorkflowNotFound(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)
	req, _ := gatewayRequestByTag(t, gateway_common.MethodWorkflowExecute, "0xffffffffffffffffffffffffffffffffffffffff") //unregistered workflow owner
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.True(t, connector.SendToGatewayCalled, "Should send error response")
	resp := connector.SendToGatewayArgs.Msg
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
	assertErrorResponse(t, connector, resp, jsonrpc.ErrInvalidRequest)
	var triggerResp gateway_common.HTTPTriggerResponse
	require.Nil(t, resp.Result, "Result should be nil in error response")
	require.Empty(t, triggerResp.WorkflowID, "WorkflowID should be empty in error response")
	require.Equal(t, req.ID, resp.ID, "Response ID should match request ID")
}

func assertErrorResponse(t *testing.T, connector *mockGatewayConnector, resp *jsonrpc.Response[json.RawMessage], code int64) {
	require.Equal(t, "gw1", connector.SendToGatewayArgs.GatewayID)
	require.Equal(t, "2.0", resp.Version)
	require.Equal(t, "id", resp.ID)
	require.Equal(t, code, resp.Error.Code)
}

func TestHandleGatewayMessage_InvalidRequest(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)
	// empty request
	req := &jsonrpc.Request[json.RawMessage]{}
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_MissingWorkflowName(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowOwner: testWorkflowOwner,
			WorkflowTag:   testWorkflowTag,
		},
		Input: json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	jsonPayloadMsg := json.RawMessage(jsonPayload)
	req := &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  gateway_common.MethodWorkflowExecute,
		Params:  &jsonPayloadMsg,
	}
	err = handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.True(t, connector.SendToGatewayCalled, "Should send error response")
	resp := connector.SendToGatewayArgs.Msg
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
	assertErrorResponse(t, connector, resp, jsonrpc.ErrInvalidRequest)
	var triggerResp gateway_common.HTTPTriggerResponse
	require.Nil(t, resp.Result, "Result should be nil in error response")
	require.Empty(t, triggerResp.WorkflowID, "WorkflowID should be empty in error response")
	require.Equal(t, req.ID, resp.ID, "Response ID should match request ID")
}

func TestHandleGatewayMessage_MissingBody(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)
	// empty request
	req := &jsonrpc.Request[json.RawMessage]{Method: gateway_common.MethodWorkflowExecute}
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_InvalidUserInputJSON(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)
	req, _ := gatewayRequest(t, gateway_common.MethodWorkflowExecute)
	invalidJSON := json.RawMessage("invalid json")
	req.Params = &invalidJSON
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_InvalidJSON(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)
	req, _ := gatewayRequest(t, gateway_common.MethodWorkflowExecute)
	params := json.RawMessage(`{"workflow":{"workflowId":"0xabcdef"},"input":{"key": {"invalid json"}}}`)
	req.Params = &params
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_UnsupportedMethod(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)

	// Create request with unsupported method
	badMethodMsg, _ := gatewayRequest(t, "unsupported_method")
	err := handler.HandleGatewayMessage(t.Context(), "gw1", badMethodMsg)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestProcessTrigger_MissingWorkflowID(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)

	// Create request with missing workflowID
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowID: "", // Empty workflowID
		},
		Input: json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	params := json.RawMessage(jsonPayload)
	req := &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  gateway_common.MethodWorkflowExecute,
		Params:  &params,
	}

	handler.processTrigger(t.Context(), "gw1", req)
	require.True(t, connector.SendToGatewayCalled, "Should send error response")
	resp := connector.SendToGatewayArgs.Msg
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
	assertErrorResponse(t, connector, resp, jsonrpc.ErrInvalidRequest)
	var triggerResp gateway_common.HTTPTriggerResponse
	require.Nil(t, resp.Result, "Result should be nil in error response")
	require.Empty(t, triggerResp.WorkflowID, "WorkflowID should be empty in error response")
	require.Equal(t, req.ID, resp.ID, "Response ID should match request ID")
}

func TestRegisterAndUnregisterWorkflow(t *testing.T) {
	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)
	_, ok := handler.workflowStore.getWorkflowByID(testWorkflowID)
	require.True(t, ok, "workflow not registered")
	err := handler.UnregisterWorkflow(context.Background(), testWorkflowID)
	require.NoError(t, err, "UnregisterWorkflow failed")
	_, ok = handler.workflowStore.getWorkflowByID(testWorkflowID)
	require.False(t, ok, "workflow still registered after unregistering")
	err = handler.UnregisterWorkflow(context.Background(), testWorkflowID)
	require.Error(t, err, "UnregisterWorkflow should return error for non-existent workflow")
}

func TestProcessTrigger_UnregisteredWorkflow(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, _ := setup(t, lggr)

	// Create request with unregistered workflowID
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowID: "nonexistent", // Workflow that doesn't exist
		},
		Input: json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	params := json.RawMessage(jsonPayload)
	req := &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  gateway_common.MethodWorkflowExecute,
		Params:  &params,
	}

	handler.processTrigger(t.Context(), "gw1", req)
	require.True(t, connector.SendToGatewayCalled, "Should send error response")

	// Verify error response
	resp := connector.SendToGatewayArgs.Msg
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
	assertErrorResponse(t, connector, resp, jsonrpc.ErrInvalidRequest)
	require.Nil(t, resp.Result, "Result should be nil in error response")
	require.Equal(t, req.ID, resp.ID)
}

func TestRegisterWorkflow_InvalidECDSAPublicKey(t *testing.T) {
	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)
	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	testCases := []struct {
		name      string
		publicKey string
		keyType   http.KeyType
		errorMsg  string
	}{
		{
			name:      "invalid publicKey format (nothex)",
			publicKey: "nothex",
			keyType:   http.KeyType_KEY_TYPE_ECDSA_EVM,
			errorMsg:  "invalid public key format",
		},
		{
			name:      "invalid publicKey length",
			publicKey: "0x123",
			keyType:   http.KeyType_KEY_TYPE_ECDSA_EVM,
			errorMsg:  "invalid public key format",
		},
		{
			name:      "invalid key type",
			publicKey: publicKey,
			keyType:   http.KeyType_KEY_TYPE_UNSPECIFIED,
			errorMsg:  "unsupported key type",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			invalidKey := &http.AuthorizedKey{
				PublicKey: tc.publicKey,
				Type:      tc.keyType,
			}
			cfg := &http.Config{
				AuthorizedKeys: []*http.AuthorizedKey{
					invalidKey,
				},
			}
			selector := gateway_common.WorkflowSelector{
				WorkflowOwner: "0xabcdef1234567890abcdef1234567890abcdef12",
				WorkflowName:  "workflowName",
				WorkflowTag:   "workflowTag",
				WorkflowID:    "workflowID",
			}
			err := handler.RegisterWorkflow(context.Background(), WorkflowRegistrationInput{
				WorkflowSelector: selector,
				Config:           cfg,
				Metadata:         WorkflowRegistrationMetadata{},
			}, sendCh)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errorMsg)
		})
	}
}

func TestRegisterWorkflow_TooManyAuthorizedKeys(t *testing.T) {
	lggr := logger.Test(t)

	// Create a custom setup with a very low max authorized keys limit
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{
		MetadataBatchSize:            10,
		MaxAuthorizedKeysPerWorkflow: 2, // Set limit to 2 keys for testing
	}
	irl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	orl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	store := newWorkflowStore(lggr)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	metadataPublisher := NewGatewayMetadataPublisher(
		lggr,
		mockConnector,
		orl,
		store,
		cfg,
		metrics,
	)
	kvstore := newTestKVStore()
	requestCache := newRequestCache(logger.Sugared(lggr), kvstore, time.Hour)
	handler, err := NewConnectorHandler(
		lggr,
		mockConnector,
		cfg,
		orl,
		irl,
		store,
		metadataPublisher,
		requestCache,
		newMetrics(t),
		nil,
	)
	require.NoError(t, err)

	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	// Test case with exactly the maximum allowed keys (should succeed)
	t.Run("exact max allowed keys", func(t *testing.T) {
		cfg := &http.Config{
			AuthorizedKeys: []*http.AuthorizedKey{
				{
					PublicKey: publicKey,
					Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
				},
				{
					PublicKey: "0xB28C9D8E47fB7b0974505D7aB544e24478B6e990",
					Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
				},
			},
		}
		selector := gateway_common.WorkflowSelector{
			WorkflowOwner: testWorkflowOwner,
			WorkflowName:  testWorkflowName,
			WorkflowTag:   testWorkflowTag,
			WorkflowID:    testWorkflowID,
		}
		err := handler.RegisterWorkflow(context.Background(), WorkflowRegistrationInput{
			WorkflowSelector: selector,
			Config:           cfg,
			Metadata:         WorkflowRegistrationMetadata{},
		}, sendCh)
		require.NoError(t, err)
	})

	// Test case with more than the maximum allowed keys (should fail)
	t.Run("too many authorized keys", func(t *testing.T) {
		cfg := &http.Config{
			AuthorizedKeys: []*http.AuthorizedKey{
				{
					PublicKey: publicKey,
					Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
				},
				{
					PublicKey: "0xB28C9D8E47fB7b0974505D7aB544e24478B6e990",
					Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
				},
				{
					PublicKey: "0xC39D8F9E47fB7b0974505D7aB544e24478B6eAA0",
					Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
				},
			},
		}
		selector := gateway_common.WorkflowSelector{
			WorkflowOwner: testWorkflowOwner,
			WorkflowName:  testWorkflowName,
			WorkflowTag:   testWorkflowTag,
			WorkflowID:    testWorkflowID,
		}
		err := handler.RegisterWorkflow(context.Background(), WorkflowRegistrationInput{
			WorkflowSelector: selector,
			Config:           cfg,
			Metadata:         WorkflowRegistrationMetadata{},
		}, sendCh)
		require.Error(t, err)
		require.Contains(t, err.Error(), "too many authorized keys: 3, max allowed: 2")
	})
}

func TestRegisterWorkflow_EmptyAuthorizedKeys(t *testing.T) {
	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)

	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	selector := gateway_common.WorkflowSelector{
		WorkflowID:    testWorkflowID,
		WorkflowOwner: testWorkflowOwner,
		WorkflowName:  testWorkflowName,
		WorkflowTag:   testWorkflowTag,
	}

	cfg := &http.Config{
		AuthorizedKeys: []*http.AuthorizedKey{},
	}

	err := handler.RegisterWorkflow(context.Background(), WorkflowRegistrationInput{
		WorkflowSelector: selector,
		Config:           cfg,
		Metadata:         WorkflowRegistrationMetadata{},
	}, sendCh)
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP trigger requires at least one authorized key")
}

func TestConnectorHandler_Start_HealthReport_Ready_Name_Close(t *testing.T) {
	lggr := logger.Test(t)
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{}
	irl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	orl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	store := newWorkflowStore(lggr)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	metadataPublisher := NewGatewayMetadataPublisher(
		lggr,
		mockConnector,
		orl,
		store,
		cfg,
		metrics,
	)
	kvstore := newTestKVStore()
	requestCache := newRequestCache(logger.Sugared(lggr), kvstore, time.Hour)
	handler, err := NewConnectorHandler(
		lggr,
		mockConnector,
		cfg,
		orl,
		irl,
		store,
		metadataPublisher,
		requestCache,
		newMetrics(t),
		nil,
	)
	require.NoError(t, err)

	require.Error(t, handler.Ready())

	ctx := context.Background()
	err = handler.Start(ctx)
	require.NoError(t, err)

	require.NoError(t, handler.Ready())

	hr := handler.HealthReport()
	require.Contains(t, hr, handler.Name())
	require.NoError(t, hr[handler.Name()])
	require.Equal(t, HandlerName, handler.Name())

	// Restarting the handler returns an error
	require.Error(t, handler.Start(ctx))

	require.NoError(t, handler.Close())
	require.Error(t, handler.Ready())

	hr = handler.HealthReport()
	require.Contains(t, hr, handler.Name())
	require.Error(t, hr[handler.Name()])

	require.Error(t, handler.Close())
}

func TestHandleGatewayMessage_PullAuthMetadata(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, _, _ := setup(t, lggr)

	// Register additional workflows to test multiple workflows
	sdkCfg2 := &http.Config{
		AuthorizedKeys: []*http.AuthorizedKey{
			{
				PublicKey: "0xB18B5D6DB47fB7b0974505D7aB544e24478B6e99",
				Type:      http.KeyType_KEY_TYPE_ECDSA_EVM,
			},
		},
	}
	triggerCh2 := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	selector := gateway_common.WorkflowSelector{
		WorkflowOwner: testWorkflowOwner2,
		WorkflowName:  testWorkflowName2,
		WorkflowTag:   testWorkflowTag,
		WorkflowID:    testWorkflowID2,
	}
	err := handler.RegisterWorkflow(t.Context(), WorkflowRegistrationInput{
		WorkflowSelector: selector,
		Config:           sdkCfg2,
		Metadata:         WorkflowRegistrationMetadata{},
	}, triggerCh2)
	require.NoError(t, err, "Failed to register second workflow")

	// Create pull auth metadata request
	// The request ID must start with the method name for validation
	requestID := gateway_common.GetRequestID(gateway_common.MethodPullWorkflowMetadata)
	req := &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      requestID,
		Method:  gateway_common.MethodPullWorkflowMetadata,
		Params:  nil, // Pull auth metadata doesn't need params
	}

	err = handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)

	require.True(t, connector.SendToGatewayCalled)
	require.Equal(t, "gw1", connector.SendToGatewayArgs.GatewayID)

	resp := connector.SendToGatewayArgs.Msg
	require.Equal(t, "2.0", resp.Version)
	require.Equal(t, requestID, resp.ID)
	require.Nil(t, resp.Error, "Response should not contain an error")
	require.NotNil(t, resp.Result, "Response should contain workflow auth metadata")

	var workflowMetadata []gateway_common.WorkflowMetadata
	err = json.Unmarshal(*resp.Result, &workflowMetadata)
	require.NoError(t, err)

	require.Len(t, workflowMetadata, 2, "Should receive auth metadata for all registered workflows")
	metadataByWorkflowID := make(map[string]gateway_common.WorkflowMetadata)
	for _, metadata := range workflowMetadata {
		metadataByWorkflowID[metadata.WorkflowSelector.WorkflowID] = metadata
	}
	wf1Metadata, exists := metadataByWorkflowID[testWorkflowID]
	require.True(t, exists, "Should contain metadata for wf1")
	require.Equal(t, testWorkflowID, wf1Metadata.WorkflowSelector.WorkflowID)
	require.Len(t, wf1Metadata.AuthorizedKeys, 1)
	require.Equal(t, strings.ToLower(publicKey), wf1Metadata.AuthorizedKeys[0].PublicKey)
	require.Equal(t, "ecdsa_evm", string(wf1Metadata.AuthorizedKeys[0].KeyType))
	wf2Metadata, exists := metadataByWorkflowID[testWorkflowID2]
	require.True(t, exists, "Should contain metadata for wf2")
	require.Equal(t, testWorkflowID2, wf2Metadata.WorkflowSelector.WorkflowID)
	require.Len(t, wf2Metadata.AuthorizedKeys, 1)
	require.Equal(t, strings.ToLower("0xB18B5D6DB47fB7b0974505D7aB544e24478B6e99"), wf2Metadata.AuthorizedKeys[0].PublicKey)
	require.Equal(t, "ecdsa_evm", string(wf2Metadata.AuthorizedKeys[0].KeyType))
}

// TestHandleGatewayMessage_PullAuthMetadata_InvalidRequestID tests pull auth metadata with invalid request ID
func TestHandleGatewayMessage_PullAuthMetadata_InvalidRequestID(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, _, _ := setup(t, lggr)

	// Create pull auth metadata request with invalid request ID
	// The request ID must start with "workflow_pull_auth_metadata" for validation
	requestID := "invalid-request-id"
	req := &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      requestID,
		Method:  gateway_common.MethodPullWorkflowMetadata,
		Params:  nil,
	}

	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)

	// Verify that SendToGateway was called with error response
	require.True(t, connector.SendToGatewayCalled)
	require.Equal(t, "gw1", connector.SendToGatewayArgs.GatewayID)

	resp := connector.SendToGatewayArgs.Msg
	require.Equal(t, "2.0", resp.Version)
	require.Equal(t, requestID, resp.ID)
	require.NotNil(t, resp.Error, "Response should contain an error")
	require.Equal(t, jsonrpc.ErrInvalidRequest, resp.Error.Code)
	require.Contains(t, resp.Error.Message, "invalid request ID")
	require.Nil(t, resp.Result, "Response should not contain result on error")
}

// TestHandleGatewayMessage_PullAuthMetadata_EmptyWorkflows tests pull auth metadata when no workflows are registered
func TestHandleGatewayMessage_PullAuthMetadata_EmptyWorkflows(t *testing.T) {
	lggr := logger.Test(t)
	// Create handler without registering any workflows
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{}
	irl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	orl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	store := newWorkflowStore(lggr)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	metadataPublisher := NewGatewayMetadataPublisher(
		lggr,
		mockConnector,
		orl,
		store,
		cfg,
		metrics,
	)
	kvstore := newTestKVStore()
	requestCache := newRequestCache(logger.Sugared(lggr), kvstore, time.Hour)
	handler, err := NewConnectorHandler(
		lggr,
		mockConnector,
		cfg,
		orl,
		irl,
		store,
		metadataPublisher,
		requestCache,
		newMetrics(t),
		nil,
	)
	require.NoError(t, err)

	// Create pull auth metadata request
	requestID := gateway_common.GetRequestID(gateway_common.MethodPullWorkflowMetadata)
	req := &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      requestID,
		Method:  gateway_common.MethodPullWorkflowMetadata,
		Params:  nil,
	}

	err = handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)

	require.False(t, mockConnector.SendToGatewayCalled)
}

func TestConnectorHandler_RequestCacheDuplicateDetection(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh, requestCache := setup(t, lggr)
	req, key := gatewayRequest(t, gateway_common.MethodWorkflowExecute)
	requireWorkflowTriggered(t, triggerCh, req, connector, handler, key, requestCache)

	// Send the same request again
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	// No trigger should be sent again
	require.Empty(t, triggerCh)
}

// mockKVStoreWithCleanupTracking tracks when PruneExpiredEntries is called
type mockKVStoreWithCleanupTracking struct {
	*testKVStore
	cleanupCalled bool
	cleanupCount  int
	mu            sync.RWMutex
}

func newMockKVStoreWithCleanupTracking() *mockKVStoreWithCleanupTracking {
	return &mockKVStoreWithCleanupTracking{
		testKVStore: newTestKVStore(),
	}
}

func (m *mockKVStoreWithCleanupTracking) PruneExpiredEntries(ctx context.Context, ttl time.Duration) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupCalled = true
	m.cleanupCount++
	return 0, nil
}

func (m *mockKVStoreWithCleanupTracking) wasCleanupCalled() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cleanupCalled
}

func (m *mockKVStoreWithCleanupTracking) getCleanupCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cleanupCount
}

func TestConnectorHandler_StartRequestCacheCleanup(t *testing.T) {
	lggr := logger.Test(t)
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{}
	irl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	orl, err := ratelimit.NewRateLimiter(rateLimiterConfig())
	require.NoError(t, err)
	store := newWorkflowStore(lggr)
	metrics, err := NewMetrics()
	require.NoError(t, err)
	metadataPublisher := NewGatewayMetadataPublisher(
		lggr,
		mockConnector,
		orl,
		store,
		cfg,
		metrics,
	)

	shortTTL := 10 * time.Millisecond
	kvstore := newMockKVStoreWithCleanupTracking()
	requestCache := newRequestCache(logger.Sugared(lggr), kvstore, shortTTL)

	handler, err := NewConnectorHandler(
		lggr,
		mockConnector,
		cfg,
		orl,
		irl,
		store,
		metadataPublisher,
		requestCache,
		newMetrics(t),
		nil,
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Start the handler - this should start the cleanup routine
	err = handler.Start(ctx)
	require.NoError(t, err)
	defer func() {
		err := handler.Close()
		require.NoError(t, err)
	}()

	// Wait for at least one cleanup cycle to run
	// The cleanup routine should run every 10ms, so we wait a bit longer
	time.Sleep(50 * time.Millisecond)

	require.True(t, kvstore.wasCleanupCalled(), "request cache cleanup should be called when handler starts")
	require.GreaterOrEqual(t, kvstore.getCleanupCount(), 1, "cleanup should be called at least once")
}

func TestRegisterWorkflow_NilInput(t *testing.T) {
	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)

	workflowSelector := gateway_common.WorkflowSelector{
		WorkflowID:    "test-workflow",
		WorkflowOwner: "test-owner",
		WorkflowName:  "test-name",
		WorkflowTag:   "test-tag",
	}

	sendCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	err := handler.RegisterWorkflow(context.Background(), WorkflowRegistrationInput{
		WorkflowSelector: workflowSelector,
		Config:           nil,
		Metadata:         WorkflowRegistrationMetadata{},
	}, sendCh)
	require.Error(t, err)
	require.Contains(t, err.Error(), "input config cannot be nil")
}

func TestProcessTrigger_NilRequest(t *testing.T) {
	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)

	// Test nil request - this should not panic
	handler.processTrigger(context.Background(), "gateway1", nil)
}

func TestHandleGatewayMessage_NilRequest(t *testing.T) {
	lggr := logger.Test(t)
	handler, _, _, _ := setup(t, lggr)

	err := handler.HandleGatewayMessage(context.Background(), "gateway1", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "request cannot be nil")
}
