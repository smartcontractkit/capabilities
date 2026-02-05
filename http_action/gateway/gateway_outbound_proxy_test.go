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
		Headers:       map[string]string{"X-Test": "1"},
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
		Headers:       map[string]string{"X-Test": "1"},
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
			Headers:       map[string]string{"X-Test": "1"},
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
			Headers:       map[string]string{"X-Test": "1"},
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
			Headers:       map[string]string{"X-Test": "1"},
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
		Headers:                 headers,
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

func TestGatewayOutboundProxy_SendRequest_MultiHeaders(t *testing.T) {
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}

	// verifyBackwardCompatibility checks that all keys in MultiHeaders are also present in Headers
	// with non-empty values, ensuring backward compatibility with the deprecated Headers field.
	verifyBackwardCompatibility := func(t *testing.T, headers map[string]string, multiHeaders map[string]*http.HeaderValues) {
		for key := range multiHeaders {
			require.NotEmpty(t, headers[key], "Headers should contain %s for backward compatibility", key)
		}
	}

	t.Run("response with multiple Set-Cookie headers from gateway", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{},
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		// Prepare gateway response with multiple Set-Cookie headers
		gatewayMultiHeaders := map[string][]string{
			"Set-Cookie": {
				"sessionid=abc123; Path=/; HttpOnly",
				"csrf_token=xyz789; Path=/; Secure",
				"pref=dark; Path=/",
			},
		}
		gatewayHeaders := map[string]string{
			"Set-Cookie": "sessionid=abc123; Path=/; HttpOnly, csrf_token=xyz789; Path=/; Secure, pref=dark; Path=/",
		}

		go func() {
			id := <-readyCh
			simulateGatewayMessageWithMultiHeaders(t, proxy, id, 200, "ok", "", true, false, false, gatewayHeaders, gatewayMultiHeaders)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(200), output.StatusCode)

		// Verify MultiHeaders contains all Set-Cookie values
		require.NotNil(t, output.MultiHeaders, "MultiHeaders should not be nil")
		setCookieHeader, ok := output.MultiHeaders["Set-Cookie"]
		require.True(t, ok, "Set-Cookie header should be in MultiHeaders")
		require.NotNil(t, setCookieHeader)
		require.Len(t, setCookieHeader.Values, 3, "Should have 3 Set-Cookie headers")
		require.Contains(t, setCookieHeader.Values, "sessionid=abc123; Path=/; HttpOnly")
		require.Contains(t, setCookieHeader.Values, "csrf_token=xyz789; Path=/; Secure")
		require.Contains(t, setCookieHeader.Values, "pref=dark; Path=/")

		// Verify Headers field has first value only (backward compatibility)
		require.Equal(t, "sessionid=abc123; Path=/; HttpOnly", output.Headers["Set-Cookie"]) //nolint:staticcheck

		// Verify backward compatibility: all keys in MultiHeaders should be in Headers
		verifyBackwardCompatibility(t, output.Headers, output.MultiHeaders) //nolint:staticcheck
	})

	t.Run("response with multiple Via headers from gateway", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{},
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		// Prepare gateway response with multiple Via headers
		gatewayMultiHeaders := map[string][]string{
			"Via": {
				"1.0 proxy1",
				"1.1 proxy2",
				"1.1 proxy3",
			},
		}
		gatewayHeaders := map[string]string{
			"Via": "1.0 proxy1, 1.1 proxy2, 1.1 proxy3",
		}

		go func() {
			id := <-readyCh
			simulateGatewayMessageWithMultiHeaders(t, proxy, id, 200, "ok", "", true, false, false, gatewayHeaders, gatewayMultiHeaders)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(200), output.StatusCode)

		// Verify MultiHeaders contains all Via values
		require.NotNil(t, output.MultiHeaders)
		viaHeader, ok := output.MultiHeaders["Via"]
		require.True(t, ok, "Via header should be in MultiHeaders")
		require.NotNil(t, viaHeader)
		require.Len(t, viaHeader.Values, 3, "Should have 3 Via headers")
		require.Contains(t, viaHeader.Values, "1.0 proxy1")
		require.Contains(t, viaHeader.Values, "1.1 proxy2")
		require.Contains(t, viaHeader.Values, "1.1 proxy3")

		// Verify Headers field has first value only (backward compatibility)
		require.Equal(t, "1.0 proxy1", output.Headers["Via"])

		// Verify backward compatibility: all keys in MultiHeaders should be in Headers
		verifyBackwardCompatibility(t, output.Headers, output.MultiHeaders)
	})

	t.Run("response with fallback to Headers when MultiHeaders not present", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{},
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		// Gateway response with only Headers (no MultiHeaders) - backward compatibility
		gatewayHeaders := map[string]string{
			"Content-Type": "application/json",
		}

		go func() {
			id := <-readyCh
			simulateGatewayMessageWithMultiHeaders(t, proxy, id, 200, "ok", "", true, false, false, gatewayHeaders, nil)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(200), output.StatusCode)

		// Verify Headers field is populated from fallback
		require.Equal(t, "application/json", output.Headers["Content-Type"])

		// MultiHeaders should be empty when not provided by gateway
		require.NotNil(t, output.MultiHeaders, "MultiHeaders should not be nil")
		require.Empty(t, output.MultiHeaders, "MultiHeaders should be empty when gateway doesn't provide it")
	})

	t.Run("response with single header value from gateway", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{},
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		// Prepare gateway response with single header value
		gatewayMultiHeaders := map[string][]string{
			"Content-Type": {"application/json"},
		}
		gatewayHeaders := map[string]string{
			"Content-Type": "application/json",
		}

		go func() {
			id := <-readyCh
			simulateGatewayMessageWithMultiHeaders(t, proxy, id, 200, "ok", "", true, false, false, gatewayHeaders, gatewayMultiHeaders)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, uint32(200), output.StatusCode)

		// Verify MultiHeaders contains single value
		require.NotNil(t, output.MultiHeaders)
		contentTypeHeader, ok := output.MultiHeaders["Content-Type"]
		require.True(t, ok, "Content-Type header should be in MultiHeaders")
		require.NotNil(t, contentTypeHeader)
		require.Len(t, contentTypeHeader.Values, 1, "Should have 1 Content-Type header")
		require.Equal(t, "application/json", contentTypeHeader.Values[0])

		// Verify Headers field matches
		require.Equal(t, "application/json", output.Headers["Content-Type"])

		// Verify backward compatibility: all keys in MultiHeaders should be in Headers
		verifyBackwardCompatibility(t, output.Headers, output.MultiHeaders)
	})
}
