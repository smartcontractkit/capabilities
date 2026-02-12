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
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"

	jsonrpc "github.com/smartcontractkit/chainlink-common/pkg/jsonrpc2"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	gateway_common "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

func newTestValidator(t *testing.T) common.RequestValidator {
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
		common.ServiceConfig{},
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
		Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
		Body:          []byte("test"),
		Timeout:       durationpb.New(5000 * time.Millisecond),
		CacheSettings: &http.CacheSettings{},
	}

	// Prepare a goroutine to receive gateway response
	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 200, "ok", "", true)
	}()

	output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
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
		Headers: map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
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

	_, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
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
		Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
		Body:          []byte("test"),
		Timeout:       durationpb.New(100 * time.Millisecond), // short timeout
		CacheSettings: &http.CacheSettings{},
	}

	// Do not send a response, should timeout
	start := time.Now()
	output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
	elapsed := time.Since(start)
	require.Error(t, err)
	require.Nil(t, output)
	assert.Contains(t, err.Error(), "request timed out")
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
		Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
		Body:          []byte("test"),
		Timeout:       durationpb.New(5000 * time.Millisecond),
		CacheSettings: &http.CacheSettings{},
	}

	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 500, "ok", "some error", true)
	}()

	output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
	require.Error(t, err)
	require.Nil(t, output)
	var userErr UserError
	assert.False(t, errors.As(err, &userErr))
	assert.Contains(t, err.Error(), "gateway returned error")
}

func TestGatewayOutboundProxy_SendRequest_UserErrors(t *testing.T) {
	t.Run("external endpoint error returns UserError", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
			Body:          []byte("test"),
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		go func() {
			id := <-readyCh
			simulateGatewayMessageWithFlags(t, proxy, id, 500, "", "endpoint failed", true, true, false)
		}()

		_, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.Error(t, err)

		var userErr UserError
		assert.True(t, errors.As(err, &userErr))
		assert.Equal(t, "endpoint failed", err.Error())
	})

	t.Run("validation error returns UserError", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
			Body:          []byte("test"),
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		go func() {
			id := <-readyCh
			// Simulate validation error
			simulateGatewayMessageWithFlags(t, proxy, id, 400, "", "invalid request format", true, false, true)
		}()

		_, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.Error(t, err)

		var userErr UserError
		assert.True(t, errors.As(err, &userErr))
		assert.Equal(t, "invalid request format", err.Error())
	})

	t.Run("response size validation error returns UserError", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
			Body:          []byte("test"),
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		oversizedBody := make([]byte, 10*1024*1024) // 10MB

		go func() {
			id := <-readyCh
			simulateGatewayMessage(t, proxy, id, 200, string(oversizedBody), "", true)
		}()

		_, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.Error(t, err)

		var userErr UserError
		assert.True(t, errors.As(err, &userErr))
	})
}

func simulateGatewayMessage(t *testing.T, proxy *gatewayOutboundProxy, id string, statusCode int, body string, errorMessage string, includeBody bool) {
	simulateGatewayMessageWithFlags(t, proxy, id, statusCode, body, errorMessage, includeBody, false, false)
}

func simulateGatewayMessageWithFlags(t *testing.T, proxy *gatewayOutboundProxy, id string, statusCode int, body string, errorMessage string, includeBody bool, isExternalError bool, isValidationError bool) {
	simulateGatewayMessageWithMultiHeaders(t, proxy, id, statusCode, body, errorMessage, includeBody, isExternalError, isValidationError, nil, nil)
}

func simulateGatewayMessageWithMultiHeaders(t *testing.T, proxy *gatewayOutboundProxy, id string, statusCode int, body string, errorMessage string, includeBody bool, isExternalError bool, isValidationError bool, headers map[string]string, multiHeaders map[string][]string) {
	req := jsonrpc.Request[json.RawMessage]{
		ID:      id,
		Method:  gateway_common.MethodHTTPAction,
		Version: "2.0",
	}
	resp := gateway_common.OutboundHTTPResponse{
		StatusCode:              statusCode,
		Body:                    []byte(body),
		ErrorMessage:            errorMessage,
		IsExternalEndpointError: isExternalError,
		IsValidationError:       isValidationError,
		Headers:                 headers, //nolint:staticcheck // Headers deprecated, gateway may send
		MultiHeaders:            multiHeaders,
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
	// CaptureSendPayload, if set, is called with the full response sent to the gateway (Result = marshalled OutboundHTTPRequest).
	CaptureSendPayload func(*jsonrpc.Response[json.RawMessage])

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
	if m.CaptureSendPayload != nil {
		m.CaptureSendPayload(resp)
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

// TestGatewayOutboundProxy_SendRequest_HeadersAndMultiHeaders covers Headers/MultiHeaders on both
// the outgoing request (cap → gateway) and the incoming response (gateway → cap).
func TestGatewayOutboundProxy_SendRequest_HeadersAndMultiHeaders(t *testing.T) {
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}

	// captureOutgoingRequest returns the OutboundHTTPRequest that was sent to the gateway.
	captureOutgoingRequest := func(t *testing.T, input *http.Request) *gateway_common.OutboundHTTPRequest {
		capturedCh := make(chan *gateway_common.OutboundHTTPRequest, 1)
		readyCh := make(chan string, 1)
		mockConnector := &mockGatewayConnector{
			GatewayIDsVal: []string{"gateway1"},
			OnSend:        func(id string) { readyCh <- id },
			CaptureSendPayload: func(resp *jsonrpc.Response[json.RawMessage]) {
				if resp.Result == nil {
					capturedCh <- nil
					return
				}
				var req gateway_common.OutboundHTTPRequest
				err := json.Unmarshal(*resp.Result, &req)
				require.NoError(t, err)
				capturedCh <- &req
			},
		}
		lggr := logger.Test(t)
		proxy, err := NewGatewayOutboundProxy(mockConnector, common.ServiceConfig{}, lggr, newMetrics(t), newTestValidator(t))
		require.NoError(t, err)
		go func() {
			id := <-readyCh
			simulateGatewayMessage(t, proxy, id, 200, "ok", "", true)
		}()
		_, _, err = proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		req := <-capturedCh
		require.NotNil(t, req, "CaptureSendPayload should have been called")
		return req
	}

	// --- Outgoing request (cap → gateway) ---

	t.Run("outgoing: error when input has both Headers and MultiHeaders", func(t *testing.T) {
		proxy, _, _ := setupSendRequestTest(t)
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "value"}, //nolint:staticcheck // Headers deprecated
			MultiHeaders:  map[string]*http.HeaderValues{"Accept": {Values: []string{"application/json"}}},
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		_, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.Error(t, err)
		var userErr UserError
		require.True(t, errors.As(err, &userErr))
		require.Contains(t, err.Error(), "either Headers or MultiHeaders, not both")
	})

	t.Run("outgoing: MultiHeaders only when input has MultiHeaders", func(t *testing.T) {
		input := &http.Request{
			Url:    "http://example.com",
			Method: "GET",
			MultiHeaders: map[string]*http.HeaderValues{
				"Accept":     {Values: []string{"application/json"}},
				"Set-Cookie": {Values: []string{"a=1", "b=2"}},
			},
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		req := captureOutgoingRequest(t, input)
		require.Len(t, req.MultiHeaders, 2)
		require.Equal(t, []string{"application/json"}, req.MultiHeaders["Accept"])
		require.Equal(t, []string{"a=1", "b=2"}, req.MultiHeaders["Set-Cookie"])
		require.Empty(t, req.Headers, "OutboundHTTPRequest must set only MultiHeaders when input has MultiHeaders") //nolint:staticcheck // Headers deprecated, testing exclusive MultiHeaders
	})

	t.Run("outgoing: Headers only when input has no MultiHeaders", func(t *testing.T) {
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "value"}, //nolint:staticcheck // Headers deprecated
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		req := captureOutgoingRequest(t, input)
		require.Equal(t, map[string]string{"X-Test": "value"}, req.Headers) //nolint:staticcheck // Headers deprecated, testing exclusive Headers
		require.Empty(t, req.MultiHeaders, "OutboundHTTPRequest must set only Headers when input has no MultiHeaders")
	})

	t.Run("outgoing: neither set when input has no headers", func(t *testing.T) {
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		req := captureOutgoingRequest(t, input)
		require.Empty(t, req.Headers) //nolint:staticcheck // Headers deprecated
		require.Empty(t, req.MultiHeaders)
	})

	// --- Incoming response (gateway → cap) ---

	t.Run("incoming: MultiHeaders preserved and Headers comma-joined", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		gatewayMultiHeaders := map[string][]string{
			"Set-Cookie": {
				"sessionid=abc123; Path=/; HttpOnly",
				"csrf_token=xyz789; Path=/; Secure",
				"pref=dark; Path=/",
			},
		}
		gatewayHeaders := map[string]string{"Set-Cookie": "sessionid=abc123; Path=/; HttpOnly"}

		go func() {
			id := <-readyCh
			simulateGatewayMessageWithMultiHeaders(t, proxy, id, 200, "ok", "", true, false, false, gatewayHeaders, gatewayMultiHeaders)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Len(t, output.MultiHeaders["Set-Cookie"].Values, 3)
		require.Contains(t, output.MultiHeaders["Set-Cookie"].Values, "sessionid=abc123; Path=/; HttpOnly")
		require.Contains(t, output.MultiHeaders["Set-Cookie"].Values, "csrf_token=xyz789; Path=/; Secure")
		require.Contains(t, output.MultiHeaders["Set-Cookie"].Values, "pref=dark; Path=/")
		require.Equal(t, "sessionid=abc123; Path=/; HttpOnly,csrf_token=xyz789; Path=/; Secure,pref=dark; Path=/", output.Headers["Set-Cookie"]) //nolint:staticcheck // Headers deprecated, comma-joined from MultiHeaders
	})

	t.Run("incoming: response always has both Headers and MultiHeaders; gateway sent only Headers", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		gatewayHeaders := map[string]string{"Content-Type": "application/json"}

		go func() {
			id := <-readyCh
			simulateGatewayMessageWithMultiHeaders(t, proxy, id, 200, "ok", "", true, false, false, gatewayHeaders, nil)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, "application/json", output.Headers["Content-Type"]) //nolint:staticcheck // Headers deprecated, testing derived from gateway Headers
		require.Len(t, output.MultiHeaders, 1, "OutboundHTTPResponse must always set both; MultiHeaders derived from Headers")
		require.Equal(t, []string{"application/json"}, output.MultiHeaders["Content-Type"].Values)
	})
}

func TestResponseHeadersFromGateway(t *testing.T) {
	t.Run("nil Headers and nil MultiHeaders returns empty maps", func(t *testing.T) {
		resp := &gateway_common.OutboundHTTPResponse{}
		headers, multiHeaders := responseHeadersFromGateway(resp)
		require.NotNil(t, headers)
		require.Empty(t, headers)
		require.NotNil(t, multiHeaders)
		require.Empty(t, multiHeaders)
	})

	t.Run("Headers only: both returned, MultiHeaders has single value per key", func(t *testing.T) {
		resp := &gateway_common.OutboundHTTPResponse{
			Headers: map[string]string{"Content-Type": "application/json", "X-Test": "value"}, //nolint:staticcheck // Headers deprecated, testing
		}
		headers, multiHeaders := responseHeadersFromGateway(resp)
		require.Equal(t, map[string]string{"Content-Type": "application/json", "X-Test": "value"}, headers)
		require.Len(t, multiHeaders, 2)
		require.Equal(t, []string{"application/json"}, multiHeaders["Content-Type"].Values)
		require.Equal(t, []string{"value"}, multiHeaders["X-Test"].Values)
	})

	t.Run("MultiHeaders only: Headers comma-joined per key", func(t *testing.T) {
		resp := &gateway_common.OutboundHTTPResponse{
			MultiHeaders: map[string][]string{
				"Set-Cookie": {"a=1", "b=2", "c=3"},
				"Accept":     {"application/json"},
			},
		}
		headers, multiHeaders := responseHeadersFromGateway(resp)
		require.Equal(t, "a=1,b=2,c=3", headers["Set-Cookie"])  //nolint:staticcheck // Headers deprecated, comma-joined
		require.Equal(t, "application/json", headers["Accept"]) //nolint:staticcheck // Headers deprecated
		require.Len(t, multiHeaders, 2)
		require.Equal(t, []string{"a=1", "b=2", "c=3"}, multiHeaders["Set-Cookie"].Values)
		require.Equal(t, []string{"application/json"}, multiHeaders["Accept"].Values)
	})

	t.Run("both set: MultiHeaders used as source, Headers ignored", func(t *testing.T) {
		resp := &gateway_common.OutboundHTTPResponse{
			Headers: map[string]string{"Content-Type": "text/plain", "X-Only": "only"}, //nolint:staticcheck // Headers deprecated, testing
			MultiHeaders: map[string][]string{
				"Content-Type": {"application/json"},
				"Set-Cookie":   {"s1", "s2"},
			},
		}
		headers, multiHeaders := responseHeadersFromGateway(resp)
		require.Equal(t, "application/json", headers["Content-Type"]) //nolint:staticcheck // from MultiHeaders
		require.Equal(t, "s1,s2", headers["Set-Cookie"])              //nolint:staticcheck // comma-joined from MultiHeaders
		require.Len(t, multiHeaders, 2)
		require.Equal(t, []string{"application/json"}, multiHeaders["Content-Type"].Values)
		require.Equal(t, []string{"s1", "s2"}, multiHeaders["Set-Cookie"].Values)
	})
}
