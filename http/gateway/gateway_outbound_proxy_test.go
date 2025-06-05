package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/http/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

func TestOutgoingConnectorHandler_AwaitConnection(t *testing.T) {
	type testCase struct {
		name string

		gatewayConnectorSetup func(*mockGatewayConnector)
		ctxSetup              func() context.Context
		expectedGateway       string
		expectedError         string
	}

	testCases := []testCase{
		{
			name: "successful connection on first try",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.AwaitErrs = nil
				mockConnector.GatewayIDsVal = []string{"gateway1", "gateway2"}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway1",
		},
		{
			name: "connection timeout then success",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.AwaitErrs = []error{errors.New("timeout"), nil}
				mockConnector.GatewayIDsVal = []string{"gateway1", "gateway2"}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway2",
		},
		{
			name: "connection timeout then success after backoff",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.GatewayIDsVal = []string{"gateway1", "gateway2"}
				mockConnector.AwaitErrs = []error{errors.New("connection failed"), errors.New("connection failed"), nil}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway1",
		},
		{
			name: "context canceled",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.GatewayIDsVal = []string{"gateway1", "gateway2"}
			},
			ctxSetup: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel() // Cancel the context immediately
				return ctx
			},
			expectedGateway: "",
			expectedError:   "context canceled",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockConnector := &mockGatewayConnector{}

			if tc.gatewayConnectorSetup != nil {
				tc.gatewayConnectorSetup(mockConnector)
			}

			c := &gatewayOutboundProxy{
				gatewayConnector: mockConnector,
				selectorOpts:     []func(*gateway_common.RoundRobinSelector){gateway_common.WithFixedStart()},
			}

			ctx := tc.ctxSetup()
			gateway, err := c.awaitConnection(ctx, logger.Test(t))
			assert.Equal(t, tc.expectedGateway, gateway)
			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// Helper for setting up proxy and mockConnector for SendRequest tests
func setupSendRequestTest(t *testing.T) (*gatewayOutboundProxy, *mockGatewayConnector, chan string) {
	readyCh := make(chan string, 1)
	mockConnector := &mockGatewayConnector{
		DonIDVal:      "don1",
		GatewayIDsVal: []string{"gateway1"},
		OnSend: func(messageID string) {
			readyCh <- messageID
		},
	}
	lggr := logger.Test(t)
	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		common.ServiceConfig{},
		lggr,
		gateway_common.WithFixedStart(),
	)
	require.NoError(t, err)
	return proxy, mockConnector, readyCh
}

func TestGatewayOutboundProxy_SendRequest_Success(t *testing.T) {
	proxy, _, readyCh := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Inputs{
		Url:       "http://example.com",
		Method:    "GET",
		Headers:   map[string]string{"X-Test": "1"},
		Body:      []byte("test"),
		TimeoutMs: 5000,
	}

	// Prepare a goroutine to receive gateway response
	go func() {
		messageID := <-readyCh
		// Wait for the request to be registered
		time.Sleep(100 * time.Millisecond)
		msg := &gateway_common.Message{
			Body: gateway_common.MessageBody{
				MessageId: messageID,
				Method:    gateway_common.MethodHTTPAction,
				Payload:   nil, // will be set below
			},
		}
		resp := gateway_common.GatewayResponse{
			StatusCode:     200,
			Body:           []byte("ok"),
			ErrorMessage:   "",
			ExecutionError: false,
		}
		payload, err := json.Marshal(resp)
		require.NoError(t, err)
		msg.Body.Payload = payload

		// Call HandleGatewayMessage to simulate the gateway response
		_ = proxy.HandleGatewayMessage(context.Background(), "gateway1", msg)
	}()

	output, err := proxy.SendRequest(context.Background(), metadata, input)
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.Equal(t, uint32(200), output.StatusCode)
	assert.Equal(t, []byte("ok"), output.Body)
	assert.Equal(t, "", output.ErrorMessage)
}

func TestGatewayOutboundProxy_SendRequest_Timeout(t *testing.T) {
	proxy, _, _ := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Inputs{
		Url:       "http://example.com",
		Method:    "GET",
		Headers:   map[string]string{"X-Test": "1"},
		Body:      []byte("test"),
		TimeoutMs: 100, // short timeout
	}

	// Do not send a response, should timeout
	start := time.Now()
	output, err := proxy.SendRequest(context.Background(), metadata, input)
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Nil(t, output)
	assert.Contains(t, err.Error(), "context deadline exceeded")
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(100))
}

func TestGatewayOutboundProxy_SendRequest_ExecutionError(t *testing.T) {
	proxy, _, readyCh := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Inputs{
		Url:       "http://example.com",
		Method:    "GET",
		Headers:   map[string]string{"X-Test": "1"},
		Body:      []byte("test"),
		TimeoutMs: 5000,
	}

	go func() {
		messageID := <-readyCh
		msg := &gateway_common.Message{
			Body: gateway_common.MessageBody{
				MessageId: messageID,
				Method:    gateway_common.MethodHTTPAction,
			},
		}
		resp := gateway_common.GatewayResponse{
			StatusCode:     500,
			Body:           nil,
			ErrorMessage:   "some error",
			ExecutionError: true,
		}
		payload, err := json.Marshal(resp)
		require.NoError(t, err)
		msg.Body.Payload = payload
		_ = proxy.HandleGatewayMessage(context.Background(), "gateway1", msg)
	}()

	output, err := proxy.SendRequest(context.Background(), metadata, input)
	require.Error(t, err)
	require.Nil(t, output)
	assert.Equal(t, "internal error", err.Error())
}

func TestGatewayOutboundProxy_SendRequest_RateLimitError(t *testing.T) {
	proxy, _, readyCh := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Inputs{
		Url:       "http://example.com",
		Method:    "GET",
		Headers:   map[string]string{"X-Test": "1"},
		Body:      []byte("test"),
		TimeoutMs: 5000,
	}

	go func() {
		messageID := <-readyCh
		msg := &gateway_common.Message{
			Body: gateway_common.MessageBody{
				MessageId: messageID,
				Method:    gateway_common.MethodHTTPAction,
			},
		}
		resp := gateway_common.GatewayResponse{
			StatusCode:     429,
			Body:           nil,
			ErrorMessage:   "global limit of outgoing gateways requests has been exceeded",
			ExecutionError: true,
		}
		payload, err := json.Marshal(resp)
		require.NoError(t, err)
		msg.Body.Payload = payload
		_ = proxy.HandleGatewayMessage(context.Background(), "gateway1", msg)
	}()

	output, err := proxy.SendRequest(context.Background(), metadata, input)
	require.Error(t, err)
	require.Nil(t, output)
	assert.Contains(t, err.Error(), "global limit of outgoing gateways requests has been exceeded")
}

type mockGatewayConnector struct {
	core.GatewayConnector
	DonIDVal      string
	GatewayIDsVal []string
	SendErr       error
	AwaitErrs     []error
	AddHandlerErr error
	OnSend        func(messageID string)

	// For tracking calls in tests
	awaitCalls []string
}

func (m *mockGatewayConnector) DonID() (string, error) {
	return m.DonIDVal, nil
}

func (m *mockGatewayConnector) GatewayIDs() ([]string, error) {
	return m.GatewayIDsVal, nil
}

func (m *mockGatewayConnector) SignAndSendToGateway(ctx context.Context, gateway string, body *gateway.MessageBody) error {
	if m.OnSend != nil {
		m.OnSend(body.MessageId)
	}
	return m.SendErr
}

func (m *mockGatewayConnector) AwaitConnection(ctx context.Context, gateway string) error {
	if len(m.AwaitErrs) == 0 {
		return nil
	}
	n := len(m.awaitCalls)
	m.awaitCalls = append(m.awaitCalls, gateway)
	return m.AwaitErrs[n]
}

func (m *mockGatewayConnector) AddHandler(methods []string, handler core.GatewayConnectorHandler) error {
	return nil
}

func TestGatewayOutboundProxy_nextBackoff(t *testing.T) {
	proxy := &gatewayOutboundProxy{
		gatewayConnectionConfig: common.GatewayConnectionConfig{
			Multiplier:       2.0,
			MaxElapsedTimeMs: 1000,
		},
	}
	b := 100 * time.Millisecond
	res := proxy.nextBackoff(b)
	assert.Equal(t, 200*time.Millisecond, res)
	res = proxy.nextBackoff(600 * time.Millisecond)
	assert.Equal(t, 1000*time.Millisecond, res) // capped at max
}
