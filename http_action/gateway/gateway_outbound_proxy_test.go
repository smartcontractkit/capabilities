package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http_action/common"
	"github.com/smartcontractkit/capabilities/http_action/validate"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

func newTestValidator(t *testing.T) common.ResponseValidator {
	lggr := logger.Test(t)
	limitsFactory := limits.Factory{
		Logger: lggr,
	}

	validator, err := validate.NewValidator(lggr, limitsFactory)
	require.NoError(t, err)
	return validator
}

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
			expectedGateway: "gateway2",
		},
		{
			name: "connection timeout then success",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.AwaitErrs = []error{errors.New("timeout"), nil}
				mockConnector.GatewayIDsVal = []string{"gateway1", "gateway2"}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway1",
		},
		{
			name: "connection timeout then success after backoff",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.GatewayIDsVal = []string{"gateway1", "gateway2"}
				mockConnector.AwaitErrs = []error{errors.New("connection failed"), errors.New("connection failed"), nil}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway2",
		},
		{
			name: "context canceled",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.GatewayIDsVal = []string{"gateway1", "gateway2"}
			},
			ctxSetup: func() context.Context {
				ctx, cancel := context.WithCancel(t.Context())
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
			}

			ctx := tc.ctxSetup()
			gateway, err := c.awaitConnection(ctx, logger.Test(t), "requestHash")
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
		OnSend: func(id string) {
			readyCh <- id
		},
	}
	lggr := logger.Test(t)
	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		common.ServiceConfig{
			IncomingRateLimiter: rateLimiterConfig(),
		},
		lggr,
		newMetrics(t),
		newTestValidator(t),
	)
	require.NoError(t, err)
	return proxy, mockConnector, readyCh
}

func newMetrics(t *testing.T) *common.Metrics {
	m, err := common.NewMetrics()
	require.NoError(t, err)
	return m
}

func TestGatewayOutboundProxy_SendRequest_Success(t *testing.T) {
	proxy, _, readyCh := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Headers:       map[string]string{"X-Test": "1"},
		Body:          []byte("test"),
		Timeout:       durationpb.New(5000 * time.Millisecond),
		CacheSettings: &http.CacheSettings{},
	}

	// Prepare a goroutine to receive gateway response
	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 200, "ok", "", true)
	}()

	output, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.Equal(t, uint32(200), output.StatusCode)
	assert.Equal(t, []byte("ok"), output.Body)
}

func TestGatewayOutboundProxy_SendRequest_MissingBodyToGateway(t *testing.T) {
	proxy, _, readyCh := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Request{
		Url:     "http://example.com",
		Method:  "GET",
		Headers: map[string]string{"X-Test": "1"},
		Body:    []byte("test"),
		Timeout: durationpb.New(5000 * time.Millisecond),
		CacheSettings: &http.CacheSettings{
			Store:  true,
			MaxAge: durationpb.New(10 * time.Second), // 10 seconds
		},
	}

	// Prepare a goroutine to receive gateway response
	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 200, "ok", "", false)
	}()

	_, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
	require.Error(t, err)
}

func TestGatewayOutboundProxy_SendRequest_Timeout(t *testing.T) {
	proxy, _, _ := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Headers:       map[string]string{"X-Test": "1"},
		Body:          []byte("test"),
		Timeout:       durationpb.New(100 * time.Millisecond), // short timeout
		CacheSettings: &http.CacheSettings{},
	}

	// Do not send a response, should timeout
	start := time.Now()
	output, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
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
	input := &http.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Headers:       map[string]string{"X-Test": "1"},
		Body:          []byte("test"),
		Timeout:       durationpb.New(5000 * time.Millisecond),
		CacheSettings: &http.CacheSettings{},
	}

	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 500, "ok", "some error", true)
	}()

	output, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
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
	input := &http.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Headers:       map[string]string{"X-Test": "1"},
		Body:          []byte("test"),
		Timeout:       durationpb.New(5000 * time.Millisecond),
		CacheSettings: &http.CacheSettings{},
	}

	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 429, "", "global limit of outgoing gateways requests has been exceeded", true)
	}()

	output, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
	require.Error(t, err)
	require.Nil(t, output)
	assert.Contains(t, err.Error(), "internal error")
}

func simulateGatewayMessage(t *testing.T, proxy *gatewayOutboundProxy, id string, statusCode int, body string, errorMessage string, includeBody bool) {
	req := jsonrpc.Request[json.RawMessage]{
		ID:      id,
		Method:  gateway_common.MethodHTTPAction,
		Version: "2.0",
	}
	resp := gateway_common.OutboundHTTPResponse{
		StatusCode:   statusCode,
		Body:         []byte(body),
		ErrorMessage: errorMessage,
	}
	if includeBody {
		payload, err := json.Marshal(resp)
		require.NoError(t, err)
		rj := json.RawMessage(payload)
		req.Params = &rj
	}

	err := proxy.HandleGatewayMessage(t.Context(), "gateway1", &req)
	require.NoError(t, err)
}

type mockGatewayConnector struct {
	core.GatewayConnector
	DonIDVal      string
	GatewayIDsVal []string
	SendErr       error
	AwaitErrs     []error
	AddHandlerErr error
	OnSend        func(id string)

	// For tracking calls in tests
	awaitCalls []string
}

func (m *mockGatewayConnector) DonID(context.Context) (string, error) {
	return m.DonIDVal, nil
}

func (m *mockGatewayConnector) GatewayIDs(context.Context) ([]string, error) {
	return m.GatewayIDsVal, nil
}

func (m *mockGatewayConnector) SendToGateway(ctx context.Context, gateway string, resp *jsonrpc.Response[json.RawMessage]) error {
	if m.OnSend != nil {
		m.OnSend(resp.ID)
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

func (m *mockGatewayConnector) AddHandler(ctx context.Context, methods []string, handler core.GatewayConnectorHandler) error {
	return m.AddHandlerErr
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
	assert.Equal(t, time.Second, res) // capped at max
}

func TestGatewayOutboundProxy_awaitConnection_RetryLimits(t *testing.T) {
	t.Run("respects context timeout - prevents infinite retry", func(t *testing.T) {
		mockConnector := &mockGatewayConnector{
			GatewayIDsVal: []string{"gateway1", "gateway2"},
			// Provide enough errors so that timeout can be triggered
			AwaitErrs: make([]error, 20),
		}
		for i := range mockConnector.AwaitErrs {
			mockConnector.AwaitErrs[i] = errors.New("connection failed")
		}

		proxy := &gatewayOutboundProxy{
			gatewayConnector: mockConnector,
			gatewayConnectionConfig: common.GatewayConnectionConfig{
				InitialIntervalMs: 50,
				MaxElapsedTimeMs:  1000,
				Multiplier:        2.0,
			},
		}

		// Set a context timeout that's shorter than what would be needed for infinite retries
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		gateway, err := proxy.awaitConnection(ctx, logger.Test(t), "testHash")

		require.Error(t, err)
		require.Contains(t, err.Error(), "context deadline exceeded")
		require.Empty(t, gateway)
	})
}

func rateLimiterConfig() ratelimit.RateLimiterConfig {
	return ratelimit.RateLimiterConfig{
		GlobalRPS:      100.0,
		GlobalBurst:    100,
		PerSenderRPS:   100.0,
		PerSenderBurst: 100,
	}
}
