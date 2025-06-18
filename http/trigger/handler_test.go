package trigger

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/jsonrpc"
)

const (
	publicKey = "0xA18B5D6DB47fB7b0974505D7aB544e24478B6e98"
)

type mockGatewayConnector struct {
	SendToGatewayCalled bool
	SendToGatewayArgs   struct {
		GatewayID string
		Msg       []byte
	}
}

func (m *mockGatewayConnector) AddHandler(ctx context.Context, methods []string, handler core.GatewayConnectorHandler) error {
	return nil
}
func (m *mockGatewayConnector) SendToGateway(ctx context.Context, gatewayID string, msg []byte) error {
	m.SendToGatewayCalled = true
	m.SendToGatewayArgs.GatewayID = gatewayID
	m.SendToGatewayArgs.Msg = msg
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

// Codec used across tests for consistency
var codec = jsonrpc.Codec{}

// gatewayRequest creates a test request message with the given method
func gatewayRequest(t *testing.T, method string) []byte {
	payload := HTTPTriggerRequest{
		Workflow: WorkflowSelector{
			WorkflowID: "wf1",
		},
		DeduplicationKey: "dedup1",
		Input:            json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	req := jsonrpc.Request{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  jsonPayload,
	}
	msg, err := codec.EncodeRequest(&req)
	require.NoError(t, err)
	return msg
}

// gatewayRequestCustomPayload creates a test request with custom payload
func gatewayRequestCustomPayload(t *testing.T, method string, payload HTTPTriggerRequest) []byte {
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)
	req := jsonrpc.Request{
		Version: "2.0",
		ID:      "id",
		Method:  method,
		Params:  jsonPayload,
	}
	msg, err := codec.EncodeRequest(&req)
	require.NoError(t, err)
	return msg
}

// Helper for setting up proxy and mockConnector for SendRequest tests
func setup(t *testing.T, lggr logger.Logger) (*requestHandler, *mockGatewayConnector, <-chan capabilities.TriggerAndId[*http.Payload]) {
	mockConnector := &mockGatewayConnector{}
	cfg := ServiceConfig{}
	handler, err := NewRequestHandler(
		lggr,
		mockConnector,
		cfg,
	)
	require.NoError(t, err)
	key := &http.AuthorizedKey_Ecdsa{
		Ecdsa: &http.ECDSAKey{
			PublicKey: publicKey,
		},
	}
	sdkCfg := &http.Config{
		AuthorizedKeys: []*http.AuthorizedKey{
			{
				Key: key,
			},
		},
	}
	triggerCh := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	err = handler.RegisterWorkflow(t.Context(), "wf1", sdkCfg, triggerCh)
	require.NoError(t, err, "Failed to register workflow")
	return handler, mockConnector, triggerCh
}

// TestHandleGatewayMessage_Success tests successful request processing
func TestHandleGatewayMessage_Success(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	msg := gatewayRequest(t, MethodHTTPTrigger)

	// Start a goroutine to assert that the correct trigger payload is received
	go func() {
		triggerReq := <-triggerCh
		input := triggerReq.Trigger.Input.AsMap()
		require.Len(t, input, 1)
		require.Equal(t, "value", input["key"])
		// TODO: PRODCRE-305 validate triggerReq.Trigger.Key
	}()

	// Process the request
	err := handler.HandleGatewayMessage(t.Context(), "gw1", msg)
	require.NoError(t, err)

	// Verify gateway connector was called with correct arguments
	require.True(t, connector.SendToGatewayCalled)
	require.Equal(t, "gw1", connector.SendToGatewayArgs.GatewayID)

	// Decode and verify response
	resp, err := codec.DecodeResponse(connector.SendToGatewayArgs.Msg)
	require.NoError(t, err)
	require.Equal(t, "2.0", resp.Version)
	require.Equal(t, "id", resp.ID)
	require.Nil(t, resp.Error, "Response should not contain an error")

	// Verify response payload
	var triggerResp HTTPTriggerResponse
	err = json.Unmarshal(resp.Result, &triggerResp)
	require.NoError(t, err)
	require.Equal(t, "wf1", triggerResp.WorkflowID)
	require.Equal(t, "dedup1", triggerResp.DeduplicationKey)

	// Verify execution ID
	executionID, err := generateExecutionID("wf1", "dedup1")
	require.NoError(t, err)
	require.Equal(t, executionID, triggerResp.WorkflowExecutionID)
}

func assertErrorResponse(t *testing.T, connector *mockGatewayConnector, resp jsonrpc.Response, code int) {
	require.Equal(t, "gw1", connector.SendToGatewayArgs.GatewayID)
	require.Equal(t, "2.0", resp.Version)
	require.Equal(t, "id", resp.ID)
	require.Equal(t, code, resp.Error.Code)
}

func TestHandleGatewayMessage_InvalidRequest(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	msg := []byte("invalid message")
	err := handler.HandleGatewayMessage(t.Context(), "gw1", msg)
	require.NoError(t, err)
	require.False(t, connector.SendToGatewayCalled)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_InvalidUserInputJSON(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	msg := `
	{
		"jsonrpc": "2.0",
		"id": "id",
		"method": "http_trigger",
		"params": {
			"workflow": {
				"workflowID": "wf1"
			},
			"deduplicationKey": "dedup1",
			"input": "invalid json"
		}
	}`
	err := handler.HandleGatewayMessage(t.Context(), "gw1", []byte(msg))
	require.NoError(t, err)
	require.True(t, connector.SendToGatewayCalled)
	resp, err := codec.DecodeResponse(connector.SendToGatewayArgs.Msg)
	require.NoError(t, err)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
	assertErrorResponse(t, connector, resp, CodeValidationError)
	var triggerResp HTTPTriggerResponse
	err = json.Unmarshal(resp.Result, &triggerResp)
	require.NoError(t, err)
	require.Empty(t, triggerResp.WorkflowID, "WorkflowID should be empty in error response")
	require.Equal(t, "dedup1", triggerResp.DeduplicationKey)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestHandleGatewayMessage_InvalidJSON(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)
	msg := `
	{
		"jsonrpc": "2.0",
		"id": "id",
		"method": "http_trigger",
		"params": {
			"workflow": {
				"workflowID": "wf1"
			},
			"deduplicationKey": "dedup1",
			"input": {
				"key": {
					"invalid json"
				}					
			}
		}
	}`
	err := handler.HandleGatewayMessage(t.Context(), "gw1", []byte(msg))
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
	payload := HTTPTriggerRequest{
		Workflow: WorkflowSelector{
			WorkflowID: "", // Empty workflowID
		},
		DeduplicationKey: "dedup1",
		Input:            json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	req := jsonrpc.Request{
		Version: "2.0",
		ID:      "id",
		Method:  MethodHTTPTrigger,
		Params:  jsonPayload,
	}

	handler.processTrigger(t.Context(), "gw1", req)
	require.True(t, connector.SendToGatewayCalled, "Should send error response")
	resp, err := codec.DecodeResponse(connector.SendToGatewayArgs.Msg)
	require.NoError(t, err)
	assertErrorResponse(t, connector, resp, CodeValidationError)
	var triggerResp HTTPTriggerResponse
	err = json.Unmarshal(resp.Result, &triggerResp)
	require.NoError(t, err)
	require.Empty(t, triggerResp.WorkflowID, "WorkflowID should be empty in error response")
	require.Equal(t, "dedup1", triggerResp.DeduplicationKey)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}

func TestRegisterAndUnregisterWorkflow(t *testing.T) {
	lggr := logger.Test(t)
	handler, _, _ := setup(t, lggr)
	_, ok := handler.workflows["wf1"]
	require.True(t, ok, "workflow not registered")
	err := handler.UnregisterWorkflow(context.Background(), "wf1")
	require.NoError(t, err, "UnregisterWorkflow failed")
	_, ok = handler.workflows["wf1"]
	require.False(t, ok, "workflow still registered after unregistering")
}

// TestProcessTrigger_UnregisteredWorkflow tests handling of unregistered workflow
func TestProcessTrigger_UnregisteredWorkflow(t *testing.T) {
	lggr := logger.Test(t)
	handler, connector, triggerCh := setup(t, lggr)

	// Create request with unregistered workflowID
	payload := HTTPTriggerRequest{
		Workflow: WorkflowSelector{
			WorkflowID: "nonexistent", // Workflow that doesn't exist
		},
		DeduplicationKey: "dedup1",
		Input:            json.RawMessage(`{"key":"value"}`),
	}
	jsonPayload, err := json.Marshal(payload)
	require.NoError(t, err)

	req := jsonrpc.Request{
		Version: "2.0",
		ID:      "id",
		Method:  MethodHTTPTrigger,
		Params:  jsonPayload,
	}

	handler.processTrigger(t.Context(), "gw1", req)
	require.True(t, connector.SendToGatewayCalled, "Should send error response")

	// Verify error response
	resp, err := codec.DecodeResponse(connector.SendToGatewayArgs.Msg)
	require.NoError(t, err)
	assertErrorResponse(t, connector, resp, CodeResourceNotFound)
	var triggerResp HTTPTriggerResponse
	err = json.Unmarshal(resp.Result, &triggerResp)
	require.NoError(t, err)
	require.Equal(t, "nonexistent", triggerResp.WorkflowID)
	require.Equal(t, "dedup1", triggerResp.DeduplicationKey)
	require.Len(t, triggerCh, 0, "trigger channel should not receive any messages")
}
