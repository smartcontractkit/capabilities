package trigger

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
)

const (
	publicKey     = "0xA18B5D6DB47fB7b0974505D7aB544e24478B6e98"
	workflowOwner = "0xabcdef1234567890abcdef1234567890abcdef12"
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
func gatewayRequest(t *testing.T, method string) *jsonrpc.Request[json.RawMessage] {
	var workflowID [32]byte
	copy(workflowID[:], []byte("wf1"))
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowID: "0x" + hex.EncodeToString(workflowID[:]),
		},
		Input: json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	jsonPayloadMsg := json.RawMessage(jsonPayload)
	return &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  &jsonPayloadMsg,
	}
}

// gatewayRequestByTag creates a test request message with the given method
func gatewayRequestByTag(t *testing.T, method string, workflowOwner string) *jsonrpc.Request[json.RawMessage] {
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowOwner: workflowOwner,
			WorkflowName:  "workflowName",
			WorkflowTag:   "workflowTag",
		},
		Input: json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	jsonPayloadMsg := json.RawMessage(jsonPayload)
	return &jsonrpc.Request[json.RawMessage]{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  &jsonPayloadMsg,
	}
}

// Helper for setting up proxy and mockConnector for SendRequest tests
func setup(t *testing.T, lggr logger.Logger) (*connectorHandler, *mockGatewayConnector, <-chan capabilities.TriggerAndId[*http.Payload]) {
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{}
	handler, err := NewConnectorHandler(
		lggr,
		mockConnector,
		cfg,
	)
	require.NoError(t, err)
	sdkCfg := &http.Config{
		AuthorizedKeys: []*http.AuthorizedKey{
			{
				PublicKey: publicKey,
				Type:      http.KeyType_KEY_TYPE_ECDSA,
			},
		},
	}
	triggerCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	var workflowID [32]byte
	copy(workflowID[:], []byte("wf1"))
	workflowIDHex := "0x" + hex.EncodeToString(workflowID[:])
	selector := gateway_common.WorkflowSelector{
		WorkflowID:    workflowIDHex,
		WorkflowOwner: "0xabcdef1234567890abcdef1234567890abcdef12",
		WorkflowName:  "workflowName",
		WorkflowTag:   "workflowTag",
	}
	err = handler.RegisterWorkflow(t.Context(), selector, sdkCfg, triggerCh)
	require.NoError(t, err, "Failed to register workflow")
	return handler, mockConnector, triggerCh
}

func requireWorkflowTriggered(t *testing.T, triggerCh <-chan capabilities.TriggerAndId[*http.Payload], req *jsonrpc.Request[json.RawMessage], connector *mockGatewayConnector, handler *connectorHandler) {
	// Start a goroutine to assert that the correct trigger payload is received
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-t.Context().Done():
			t.Errorf("Test context was cancelled before trigger was received")
		case triggerReq := <-triggerCh:
			input := triggerReq.Trigger.Input.AsMap()
			require.Len(t, input, 1)
			require.Equal(t, "value", input["key"])
			// TODO: PRODCRE-305 validate triggerReq.Trigger.Key
		}
	}()
	var workflowID [32]byte
	copy(workflowID[:], []byte("wf1"))
	workflowIDHex := "0x" + hex.EncodeToString(workflowID[:])
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
	require.Equal(t, workflowIDHex, triggerResp.WorkflowID)

	executionID, err := workflows.EncodeExecutionID(workflowIDHex, req.ID)
	require.NoError(t, err)
	require.Equal(t, executionID, triggerResp.WorkflowExecutionID)
	select {
	case <-t.Context().Done():
		t.Errorf("Test context was cancelled before trigger was received")
	case <-done: // Ensure goroutine completes
	}
}

// TestHandleGatewayMessage_Success tests successful request processing
func TestHandleGatewayMessage_Success(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	req := gatewayRequest(t, gateway_common.MethodWorkflowExecute)
	requireWorkflowTriggered(t, triggerCh, req, connector, handler)
}

// TestHandleGatewayMessage_ByTag tests successful request processing using
// workflowOwner/Name/Tag combination
func TestHandleGatewayMessage_ByTag(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	req := gatewayRequestByTag(t, gateway_common.MethodWorkflowExecute, workflowOwner)
	requireWorkflowTriggered(t, triggerCh, req, connector, handler)
}

func TestHandleGatewayMessage_ByTag_WorkflowNotFound(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	req := gatewayRequestByTag(t, gateway_common.MethodWorkflowExecute, "0xffffffffffffffffffffffffffffffffffffffff") //unregistered workflow owner
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
	handler, connector, triggerCh := setup(t, lggr)
	// empty request
	req := &jsonrpc.Request[json.RawMessage]{}
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_MissingWorkflowName(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	var workflowID [32]byte
	copy(workflowID[:], []byte("wf1"))
	payload := gateway_common.HTTPTriggerRequest{
		Workflow: gateway_common.WorkflowSelector{
			WorkflowOwner: workflowOwner,
			WorkflowTag:   "workflowTag",
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
	handler, connector, triggerCh := setup(t, lggr)
	// empty request
	req := &jsonrpc.Request[json.RawMessage]{Method: gateway_common.MethodWorkflowExecute}
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_InvalidUserInputJSON(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	req := gatewayRequest(t, gateway_common.MethodWorkflowExecute)
	invalidJSON := json.RawMessage("invalid json")
	req.Params = &invalidJSON
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_InvalidJSON(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	req := gatewayRequest(t, gateway_common.MethodWorkflowExecute)
	params := json.RawMessage(`{"workflow":{"workflowId":"wf1"},"input":{"key": {"invalid json"}}}`)
	req.Params = &params
	err := handler.HandleGatewayMessage(t.Context(), "gw1", req)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_UnsupportedMethod(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)

	// Create request with unsupported method
	badMethodMsg := gatewayRequest(t, "unsupported_method")
	err := handler.HandleGatewayMessage(t.Context(), "gw1", badMethodMsg)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestProcessTrigger_MissingWorkflowID(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)

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
	handler, _, _ := setup(t, lggr)
	var workflowID [32]byte
	copy(workflowID[:], []byte("wf1"))
	workflowIDHex := "0x" + hex.EncodeToString(workflowID[:])
	_, ok := handler.workflowStore.getWorkflowByID(workflowIDHex)
	require.True(t, ok, "workflow not registered")
	err := handler.UnregisterWorkflow(context.Background(), workflowIDHex)
	require.NoError(t, err, "UnregisterWorkflow failed")
	_, ok = handler.workflowStore.getWorkflowByID(workflowIDHex)
	require.False(t, ok, "workflow still registered after unregistering")
	err = handler.UnregisterWorkflow(context.Background(), workflowIDHex)
	require.Error(t, err, "UnregisterWorkflow should return error for non-existent workflow")
}

func TestProcessTrigger_UnregisteredWorkflow(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)

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
	handler, _, _ := setup(t, lggr)
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
			keyType:   http.KeyType_KEY_TYPE_ECDSA,
			errorMsg:  "invalid public key format",
		},
		{
			name:      "invalid publicKey length",
			publicKey: "0x123",
			keyType:   http.KeyType_KEY_TYPE_ECDSA,
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
			err := handler.RegisterWorkflow(context.Background(), selector, cfg, sendCh)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.errorMsg)
		})
	}
}
func TestConnectorHandler_Start_HealthReport_Ready_Name_Close(t *testing.T) {
	lggr := logger.Test(t)
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{}
	handler, err := NewConnectorHandler(lggr, mockConnector, cfg)
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
