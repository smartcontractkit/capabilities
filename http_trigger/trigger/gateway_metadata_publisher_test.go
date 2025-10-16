package trigger

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/triggers/http"
	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

type testGatewayConnector struct {
	gatewayIDs         []string
	gatewayIDsError    error
	sendToGatewayError error
	sendToGatewayCalls []sendToGatewayCall
}

type sendToGatewayCall struct {
	gatewayID string
	msg       *jsonrpc.Response[json.RawMessage]
}

func (m *testGatewayConnector) GatewayIDs(ctx context.Context) ([]string, error) {
	return m.gatewayIDs, m.gatewayIDsError
}

func (m *testGatewayConnector) SendToGateway(ctx context.Context, gatewayID string, msg *jsonrpc.Response[json.RawMessage]) error {
	m.sendToGatewayCalls = append(m.sendToGatewayCalls, sendToGatewayCall{
		gatewayID: gatewayID,
		msg:       msg,
	})
	return m.sendToGatewayError
}

func (m *testGatewayConnector) AddHandler(ctx context.Context, methods []string, handler core.GatewayConnectorHandler) error {
	return nil
}

func (m *testGatewayConnector) RemoveHandler(ctx context.Context, methods []string) error { return nil }

func (m *testGatewayConnector) Close() error {
	return nil
}

func (m *testGatewayConnector) Start(ctx context.Context) error {
	return nil
}

func (m *testGatewayConnector) Name() string {
	return "testGatewayConnector"
}

func (m *testGatewayConnector) Ready() error {
	return nil
}

func (m *testGatewayConnector) HealthReport() map[string]error {
	return map[string]error{}
}

func (m *testGatewayConnector) AwaitConnection(ctx context.Context, gatewayID string) error {
	return nil
}

func (m *testGatewayConnector) DonID(ctx context.Context) (string, error) {
	return "test-don-id", nil
}

func (m *testGatewayConnector) SignMessage(ctx context.Context, msg []byte) ([]byte, error) {
	return msg, nil
}

func createTestGatewayAuthPublisher(t *testing.T) (*gatewayMetadataPublisher, *testGatewayConnector, *workflowStore, *ratelimit.RateLimiter) {
	lggr := logger.Test(t)
	gc := &testGatewayConnector{
		gatewayIDs: []string{"gateway1", "gateway2"},
	}
	workflowStore := newWorkflowStore(lggr)

	rateLimiterConfig := ratelimit.RateLimiterConfig{
		GlobalRPS:      100.0,
		GlobalBurst:    100,
		PerSenderRPS:   100.0,
		PerSenderBurst: 100,
	}
	rateLimiter, err := ratelimit.NewRateLimiter(rateLimiterConfig)
	require.NoError(t, err)

	cfg := ServiceConfig{
		MetadataBatchSize: 10,
		GatewayConnectionConfig: GatewayConnectionConfig{
			MaxPushMetadataDurationMs: 5000,
			MaxPullMetadataDurationMs: 5000,
			RetryConfig: RetryConfig{
				InitialIntervalMs: 100,
				MaxIntervalTimeMs: 5000,
				Multiplier:        2.0,
			},
		},
	}
	metrics, err := NewMetrics()
	require.NoError(t, err)
	publisher := NewGatewayMetadataPublisher(lggr, gc, rateLimiter, workflowStore, cfg, metrics)

	return publisher, gc, workflowStore, rateLimiter
}

func requireSendToGatewayCall(t *testing.T, call sendToGatewayCall, gatewayID string, workflowSelector gateway.WorkflowSelector, keys []gateway.AuthorizedKey) {
	require.Equal(t, gatewayID, call.gatewayID)
	var authMetadata gateway.WorkflowMetadata
	err := json.Unmarshal(*call.msg.Result, &authMetadata)
	require.NoError(t, err)
	require.Equal(t, workflowSelector, authMetadata.WorkflowSelector)
	require.Equal(t, keys, authMetadata.AuthorizedKeys)
}

func TestBroadcastWorkflow_Success(t *testing.T) {
	t.Parallel()

	publisher, gc, _, _ := createTestGatewayAuthPublisher(t)

	workflowSelector := gateway.WorkflowSelector{
		WorkflowID:    "test-workflow-123",
		WorkflowOwner: "test-owner",
		WorkflowName:  "test-name",
		WorkflowTag:   "test-tag",
	}
	keys := []gateway.AuthorizedKey{
		{
			KeyType:   "ECDSA",
			PublicKey: "0x1234567890abcdef",
		},
		{
			KeyType:   "ECDSA",
			PublicKey: "0xabcdef1234567890",
		},
	}
	err := publisher.BroadcastWorkflowMetadata(t.Context(), workflowSelector, keys)

	require.NoError(t, err)

	// Verify SendToGateway was called for each gateway
	calls := gc.sendToGatewayCalls
	require.Equal(t, 2, len(calls))
	requireSendToGatewayCall(t, calls[0], "gateway1", workflowSelector, keys)
	requireSendToGatewayCall(t, calls[1], "gateway2", workflowSelector, keys)
}

func TestBroadcastWorkflow_GatewayIDsError(t *testing.T) {
	t.Parallel()

	publisher, gc, _, _ := createTestGatewayAuthPublisher(t)

	workflowSelector := gateway.WorkflowSelector{
		WorkflowID:    "test-workflow-123",
		WorkflowOwner: "test-owner",
		WorkflowName:  "test-name",
		WorkflowTag:   "test-tag",
	}
	keys := []gateway.AuthorizedKey{}
	expectedError := fmt.Errorf("gateway connection failed")

	gc.gatewayIDsError = expectedError
	err := publisher.BroadcastWorkflowMetadata(t.Context(), workflowSelector, keys)

	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to get gateway IDs")
}

func TestBroadcastWorkflow_SendToGatewayError(t *testing.T) {
	t.Parallel()

	publisher, gc, _, _ := createTestGatewayAuthPublisher(t)

	workflowSelector := gateway.WorkflowSelector{
		WorkflowID:    "test-workflow-123",
		WorkflowOwner: "test-owner",
		WorkflowName:  "test-name",
		WorkflowTag:   "test-tag",
	}
	keys := []gateway.AuthorizedKey{}
	expectedError := fmt.Errorf("send failed")

	gc.sendToGatewayError = expectedError
	err := publisher.BroadcastWorkflowMetadata(t.Context(), workflowSelector, keys)

	require.Error(t, err)
	require.Contains(t, err.Error(), "context canceled while awaiting connection to gateway")
	require.Greater(t, len(gc.sendToGatewayCalls), len(gc.gatewayIDs)) // Should attempt to send more than the number of gateways
}

func TestSendWorkflows_Success(t *testing.T) {
	t.Parallel()

	publisher, gc, workflowStore, _ := createTestGatewayAuthPublisher(t)

	// Register some test workflows
	authorizedKeys1 := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x1234"},
	}
	authorizedKeys2 := []gateway.AuthorizedKey{
		{KeyType: "ECDSA", PublicKey: "0x1235"},
		{KeyType: "ECDSA", PublicKey: "0x1236"},
	}
	sendCh1 := make(chan capabilities.TriggerAndId[*http.Payload], 1)
	sendCh2 := make(chan capabilities.TriggerAndId[*http.Payload], 1)

	selector1 := gateway.WorkflowSelector{
		WorkflowID:    testWorkflowID1,
		WorkflowOwner: testWorkflowOwner1,
		WorkflowName:  testWorkflowName1,
		WorkflowTag:   "tag1",
	}
	selector2 := gateway.WorkflowSelector{
		WorkflowID:    testWorkflowID2,
		WorkflowOwner: testWorkflowOwner2,
		WorkflowName:  testWorkflowName2,
		WorkflowTag:   "tag2",
	}

	wf1 := newWorkflow(selector1, authorizedKeys1, sendCh1)
	wf2 := newWorkflow(selector2, authorizedKeys2, sendCh2)

	err := workflowStore.upsertWorkflow(wf1)
	require.NoError(t, err)
	err = workflowStore.upsertWorkflow(wf2)
	require.NoError(t, err)

	gatewayID := "gateway1"
	rawParams := json.RawMessage(`{}`)
	req := &jsonrpc.Request[json.RawMessage]{
		ID:     gateway.GetRequestID(gateway.MethodPullWorkflowMetadata, "requestID"),
		Method: "test",
		Params: &rawParams,
	}
	err = publisher.SendWorkflowMetadata(t.Context(), gatewayID, req)
	require.NoError(t, err)

	calls := gc.sendToGatewayCalls
	require.Equal(t, 1, len(calls))
	var authMetadata []gateway.WorkflowMetadata
	err = json.Unmarshal(*calls[0].msg.Result, &authMetadata)
	require.NoError(t, err)
	require.Len(t, authMetadata, 2)
	found1, found2 := false, false
	for _, metadata := range authMetadata {
		switch metadata.WorkflowSelector.WorkflowID {
		case testWorkflowID1:
			require.Equal(t, authorizedKeys1, metadata.AuthorizedKeys)
			found1 = true
		case testWorkflowID2:
			require.ElementsMatch(t, authorizedKeys2, metadata.AuthorizedKeys)
			found2 = true
		}
	}
	require.True(t, found1, "workflow1 metadata not found in response")
	require.True(t, found2, "workflow2 metadata not found in response")
}

func TestSendWorkflows_EmptyWorkflows(t *testing.T) {
	t.Parallel()

	publisher, _, _, _ := createTestGatewayAuthPublisher(t)

	gatewayID := "gateway1"
	rawParams2 := json.RawMessage(`{}`)
	req := &jsonrpc.Request[json.RawMessage]{
		ID:     gateway.GetRequestID(gateway.MethodPullWorkflowMetadata, "requestID"),
		Method: "test",
		Params: &rawParams2,
	}
	err := publisher.SendWorkflowMetadata(t.Context(), gatewayID, req)
	require.NoError(t, err)
}

func TestSendWorkflows_InvalidRequestID(t *testing.T) {
	t.Parallel()

	publisher, _, _, _ := createTestGatewayAuthPublisher(t)

	gatewayID := "gateway1"
	rawParams2 := json.RawMessage(`{}`)
	req := &jsonrpc.Request[json.RawMessage]{
		ID:     "invalid-request-id",
		Method: "test",
		Params: &rawParams2,
	}
	err := publisher.SendWorkflowMetadata(t.Context(), gatewayID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid request ID for workflow pull metadata")
}

func TestNextBackoff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		backoff     time.Duration
		multiplier  float64
		maxDuration time.Duration
		expected    time.Duration
	}{
		{
			name:        "normal backoff",
			backoff:     100 * time.Millisecond,
			multiplier:  2.0,
			maxDuration: 10 * time.Second,
			expected:    200 * time.Millisecond,
		},
		{
			name:        "backoff exceeds max",
			backoff:     5 * time.Second,
			multiplier:  2.0,
			maxDuration: 8 * time.Second,
			expected:    8 * time.Second,
		},
		{
			name:        "zero backoff",
			backoff:     0,
			multiplier:  2.0,
			maxDuration: 10 * time.Second,
			expected:    0,
		},
		{
			name:        "fractional multiplier",
			backoff:     1000 * time.Millisecond,
			multiplier:  1.5,
			maxDuration: 10 * time.Second,
			expected:    1500 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := nextBackoff(tt.backoff, tt.multiplier, tt.maxDuration)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestSendWorkflowMetadata_NilRequest(t *testing.T) {
	testGatewayConnector := &testGatewayConnector{}
	rateLimiterConfig := ratelimit.RateLimiterConfig{
		GlobalRPS:      100.0,
		GlobalBurst:    100,
		PerSenderRPS:   100.0,
		PerSenderBurst: 100,
	}
	outgoingRateLimiter, err := ratelimit.NewRateLimiter(rateLimiterConfig)
	require.NoError(t, err)

	workflowStore := newWorkflowStore(logger.Test(t))
	cfg := ServiceConfig{}
	metrics, err := NewMetrics()
	require.NoError(t, err)
	publisher := NewGatewayMetadataPublisher(logger.Test(t), testGatewayConnector, outgoingRateLimiter, workflowStore, cfg, metrics)

	// Test nil request
	err = publisher.SendWorkflowMetadata(t.Context(), "gateway1", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "request cannot be nil")
}
