package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"google.golang.org/protobuf/proto"
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

var testTimeout = durationpb.New(5000 * time.Millisecond)

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
				mockConnector.Gateways = []mockGatewayEntry{
					{ID: "gateway1"},
					{ID: "gateway2"},
				}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway2",
		},
		{
			name: "connection timeout then success",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.AwaitErrs = []error{errors.New("timeout"), nil}
				mockConnector.Gateways = []mockGatewayEntry{
					{ID: "gateway1"},
					{ID: "gateway2"},
				}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway1",
		},
		{
			name: "connection timeout then success after backoff",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.Gateways = []mockGatewayEntry{
					{ID: "gateway1"},
					{ID: "gateway2"},
				}
				mockConnector.AwaitErrs = []error{errors.New("connection failed"), errors.New("connection failed"), nil}
			},
			ctxSetup:        context.Background,
			expectedGateway: "gateway2",
		},
		{
			name: "context canceled",
			gatewayConnectorSetup: func(mockConnector *mockGatewayConnector) {
				mockConnector.Gateways = []mockGatewayEntry{
					{ID: "gateway1"},
					{ID: "gateway2"},
				}
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
				metrics:          newMetrics(t),
			}

			ctx := tc.ctxSetup()
			gateway, err := c.awaitConnection(ctx, logger.Test(t), "", "requestHash")
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
	return setupSendRequestTestWithConfig(t, common.ServiceConfig{})
}

func setupSendRequestTestWithConfig(t *testing.T, cfg common.ServiceConfig) (*gatewayOutboundProxy, *mockGatewayConnector, chan string) {
	return setupSendRequestTestWithMetrics(t, cfg, newMetrics(t))
}

func setupSendRequestTestWithMetrics(t *testing.T, cfg common.ServiceConfig, metrics *common.Metrics) (*gatewayOutboundProxy, *mockGatewayConnector, chan string) {
	readyCh := make(chan string, 1)
	mockConnector := &mockGatewayConnector{
		SourceDonID: "don1",
		Gateways: []mockGatewayEntry{
			{ID: "gateway1"},
		},
		OnSend: func(id string) {
			readyCh <- id
		},
	}
	lggr := logger.Test(t)
	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		cfg,
		lggr,
		metrics,
		newTestValidator(t),
	)
	require.NoError(t, err)
	return proxy, mockConnector, readyCh
}

func newMetrics(t *testing.T) *common.Metrics {
	m, err := common.NewMetrics(noop.Meter{})
	require.NoError(t, err)
	return m
}

func newMetricsWithReader(t *testing.T) (*common.Metrics, *sdkmetric.ManualReader) {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() { require.NoError(t, meterProvider.Shutdown(context.Background())) })
	m, err := common.NewMetrics(meterProvider.Meter("http-action-test"))
	require.NoError(t, err)
	return m, reader
}

func findHistogramDataPoint(t *testing.T, rm metricdata.ResourceMetrics, name string, attrs map[string]string) (metricdata.HistogramDataPoint[int64], bool) {
	t.Helper()
	for _, scopeMetrics := range rm.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			if m.Name != name {
				continue
			}
			histogram, ok := m.Data.(metricdata.Histogram[int64])
			if !ok {
				continue
			}
			for _, dp := range histogram.DataPoints {
				if attributesMatch(dp.Attributes, attrs) {
					return dp, true
				}
			}
		}
	}
	return metricdata.HistogramDataPoint[int64]{}, false
}

func sumCounterByAttrs(rm metricdata.ResourceMetrics, name string, attrs map[string]string) int64 {
	var total int64
	for _, scopeMetrics := range rm.ScopeMetrics {
		for _, m := range scopeMetrics.Metrics {
			if m.Name != name {
				continue
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				continue
			}
			for _, dp := range sum.DataPoints {
				if attributesMatch(dp.Attributes, attrs) {
					total += dp.Value
				}
			}
		}
	}
	return total
}

func attributesMatch(set attribute.Set, attrs map[string]string) bool {
	for k, v := range attrs {
		attributeValue, ok := set.Value(attribute.Key(k))
		if !ok || attributeValue.AsString() != v {
			return false
		}
	}
	return true
}

func TestGatewayOutboundProxy_SendRequest_RoundTripMetrics(t *testing.T) {
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	newInput := func(timeout time.Duration) *http.Request {
		return &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte("test"),
			Timeout:       durationpb.New(timeout),
			CacheSettings: &http.CacheSettings{},
		}
	}
	gatewayAttr := map[string]string{common.AttrGatewayID: "gateway1"}

	t.Run("delayed gateway response appears in round trip, recorded once", func(t *testing.T) {
		metrics, reader := newMetricsWithReader(t)
		proxy, _, readyCh := setupSendRequestTestWithMetrics(t, common.ServiceConfig{}, metrics)

		const responseDelay = 200 * time.Millisecond
		go func() {
			id := <-readyCh
			time.Sleep(responseDelay)
			simulateGatewayMessage(t, proxy, id, 200, "ok", "", true)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, newInput(5*time.Second), time.Now())
		require.NoError(t, err)
		require.NotNil(t, output)

		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &rm))
		dp, ok := findHistogramDataPoint(t, rm, "http_action_capability_gateway_round_trip_ms", gatewayAttr)
		require.True(t, ok, "round trip must be recorded for the selected gateway")
		require.Equal(t, uint64(1), dp.Count, "each completed round trip must record exactly once")
		require.GreaterOrEqual(t, dp.Sum, int64(150), "round trip must include the gateway response delay")
		require.Zero(t, sumCounterByAttrs(rm, "http_action_capability_gateway_round_trip_failures", gatewayAttr))
	})

	t.Run("error response is still recorded as a round trip", func(t *testing.T) {
		metrics, reader := newMetricsWithReader(t)
		proxy, _, readyCh := setupSendRequestTestWithMetrics(t, common.ServiceConfig{}, metrics)

		go func() {
			id := <-readyCh
			simulateGatewayMessage(t, proxy, id, 500, "", "some error", true)
		}()

		_, _, err := proxy.SendRequest(t.Context(), metadata, newInput(5*time.Second), time.Now())
		require.Error(t, err)

		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &rm))
		dp, ok := findHistogramDataPoint(t, rm, "http_action_capability_gateway_round_trip_ms", gatewayAttr)
		require.True(t, ok, "received error responses must also record the round trip")
		require.Equal(t, uint64(1), dp.Count)
	})

	t.Run("send error increments failure counter, no duration sample", func(t *testing.T) {
		metrics, reader := newMetricsWithReader(t)
		proxy, mockConnector, _ := setupSendRequestTestWithMetrics(t, common.ServiceConfig{}, metrics)
		mockConnector.SendErr = errors.New("boom")

		_, _, err := proxy.SendRequest(t.Context(), metadata, newInput(5*time.Second), time.Now())
		require.ErrorContains(t, err, "failed to send request to gateway")

		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &rm))
		require.Equal(t, int64(1), sumCounterByAttrs(rm, "http_action_capability_gateway_round_trip_failures",
			map[string]string{common.AttrGatewayID: "gateway1", common.AttrReason: common.RoundTripReasonSendError}))
		_, ok := findHistogramDataPoint(t, rm, "http_action_capability_gateway_round_trip_ms", gatewayAttr)
		require.False(t, ok, "failed sends must not appear in the completed-response histogram")
	})

	t.Run("context done increments failure counter, no duration sample", func(t *testing.T) {
		metrics, reader := newMetricsWithReader(t)
		proxy, _, readyCh := setupSendRequestTestWithMetrics(t, common.ServiceConfig{
			GatewayConnectionConfig: common.GatewayConnectionConfig{ResponseGraceMs: 100},
		}, metrics)

		// Never respond on behalf of the gateway; the wait context times out.
		go func() { <-readyCh }()

		_, _, err := proxy.SendRequest(t.Context(), metadata, newInput(100*time.Millisecond), time.Now())
		require.Error(t, err)
		var timeoutErr TimeoutError
		require.True(t, errors.As(err, &timeoutErr), "existing timeout error behavior must be preserved")

		var rm metricdata.ResourceMetrics
		require.NoError(t, reader.Collect(t.Context(), &rm))
		require.Equal(t, int64(1), sumCounterByAttrs(rm, "http_action_capability_gateway_round_trip_failures",
			map[string]string{common.AttrGatewayID: "gateway1", common.AttrReason: common.RoundTripReasonContextDone}))
		_, ok := findHistogramDataPoint(t, rm, "http_action_capability_gateway_round_trip_ms", gatewayAttr)
		require.False(t, ok, "failed waits must not appear in the completed-response histogram")
	})
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
		Timeout:       testTimeout,
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
		Timeout: testTimeout,
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
		Timeout:       testTimeout,
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
			Timeout:       testTimeout,
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
			Timeout:       testTimeout,
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
			Timeout:       testTimeout,
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

	t.Run("caller cancellation returns CanceledError", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTest(t)

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &http.Request{
			Url:    "http://example.com",
			Method: "GET",
			MultiHeaders: map[string]*http.HeaderValues{
				"X-Test": {
					Values: []string{"1"},
				},
			},
			Body:          []byte("test"),
			Timeout:       testTimeout,
			CacheSettings: &http.CacheSettings{},
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		errCh := make(chan error, 1)
		var output *http.Response
		go func() {
			var err error
			output, _, err = proxy.SendRequest(ctx, metadata, input, time.Now())
			errCh <- err
		}()

		// cancel after SendToGateway ran
		<-readyCh
		cancel()

		err := <-errCh
		require.Error(t, err)
		require.Nil(t, output)
		assert.Contains(t, err.Error(), ErrMsgGatewayResponseWait)
		assert.Contains(t, err.Error(), "context canceled")

		var canceledErr CanceledError
		assert.True(t, errors.As(err, &canceledErr))
		var userErr UserError
		assert.False(t, errors.As(err, &userErr))
		var timeoutErr TimeoutError
		assert.False(t, errors.As(err, &timeoutErr))
	})

	t.Run("no gateway response returns TimeoutError", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTestWithConfig(t, common.ServiceConfig{
			GatewayConnectionConfig: common.GatewayConnectionConfig{ResponseGraceMs: 100},
		})

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte("test"),
			Timeout:       durationpb.New(200 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}

		// Never respond on behalf of the gateway; readyCh is buffered so the send does not block.
		_ = readyCh

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.Error(t, err)
		require.Nil(t, output)
		assert.Contains(t, err.Error(), ErrMsgGatewayResponseTimeout)

		var timeoutErr TimeoutError
		assert.True(t, errors.As(err, &timeoutErr))
		var userErr UserError
		assert.False(t, errors.As(err, &userErr))
	})

	// The regression the grace period exists for: a classified response necessarily lands after the
	// request timeout, and must still be honored rather than reported as a platform timeout.
	t.Run("gateway response after request timeout is still classified", func(t *testing.T) {
		proxy, _, readyCh := setupSendRequestTestWithConfig(t, common.ServiceConfig{
			GatewayConnectionConfig: common.GatewayConnectionConfig{ResponseGraceMs: 5_000},
		})

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		requestTimeout := 200 * time.Millisecond
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte("test"),
			Timeout:       durationpb.New(requestTimeout),
			CacheSettings: &http.CacheSettings{},
		}

		go func() {
			id := <-readyCh
			// The delay is the subject of the test, not a synchronization device.
			time.Sleep(2 * requestTimeout)
			simulateGatewayMessageWithFlags(t, proxy, id, 0, "", "endpoint timed out", true, true, false)
		}()

		output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.Error(t, err)
		require.Nil(t, output)
		assert.Contains(t, err.Error(), "endpoint timed out")
		assert.NotContains(t, err.Error(), ErrMsgGatewayResponseWait)
		assert.NotContains(t, err.Error(), ErrMsgGatewayResponseTimeout)

		var userErr UserError
		assert.True(t, errors.As(err, &userErr), "late gateway response must be classified, not timed out")
		var timeoutErr TimeoutError
		assert.False(t, errors.As(err, &timeoutErr))
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

type mockGatewayEntry struct {
	ID    string
	DonID string
}

type mockGatewayConnector struct {
	core.GatewayConnector
	SourceDonID   string
	Gateways      []mockGatewayEntry
	SendErr       error
	AwaitErrs     []error
	AddHandlerErr error
	OnSend        func(id string)
	// CaptureSendPayload, if set, is called with the full response sent to the gateway (Result = marshalled OutboundHTTPRequest).
	CaptureSendPayload func(*jsonrpc.Response[json.RawMessage])

	// For tracking calls in tests
	awaitCalls []string
}

func (m *mockGatewayConnector) multiDonMode() bool {
	for _, gw := range m.Gateways {
		if gw.DonID != "" {
			return true
		}
	}
	return false
}

func (m *mockGatewayConnector) gatewayIDsForDon(donID string) []string {
	if donID == "" {
		ids := make([]string, len(m.Gateways))
		for i, gw := range m.Gateways {
			ids[i] = gw.ID
		}
		return ids
	}

	if !m.multiDonMode() {
		return nil
	}

	var ids []string
	for _, gw := range m.Gateways {
		if gw.DonID == donID {
			ids = append(ids, gw.ID)
		}
	}
	return ids
}

func (m *mockGatewayConnector) DonID(context.Context) (string, error) {
	return m.SourceDonID, nil
}

func (m *mockGatewayConnector) GatewayIDs(context.Context) ([]string, error) {
	return m.gatewayIDsForDon(""), nil
}

func (m *mockGatewayConnector) GatewayIDsForDon(_ context.Context, donID string) ([]string, error) {
	return m.gatewayIDsForDon(donID), nil
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

func (m *mockGatewayConnector) RemoveHandler(context.Context, []string) error {
	return nil
}

func (m *mockGatewayConnector) SignMessage(context.Context, []byte) ([]byte, error) {
	return nil, nil
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
			Gateways: []mockGatewayEntry{
				{ID: "gateway1"},
				{ID: "gateway2"},
			},
			// Provide enough errors so that timeout can be triggered
			AwaitErrs: make([]error, 20),
		}
		for i := range mockConnector.AwaitErrs {
			mockConnector.AwaitErrs[i] = errors.New("connection failed")
		}

		proxy := &gatewayOutboundProxy{
			gatewayConnector: mockConnector,
			metrics:          newMetrics(t),
			gatewayConnectionConfig: common.GatewayConnectionConfig{
				InitialIntervalMs: 50,
				MaxElapsedTimeMs:  1000,
				Multiplier:        2.0,
			},
		}

		// Set a context timeout that's shorter than what would be needed for infinite retries
		ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
		defer cancel()
		gateway, err := proxy.awaitConnection(ctx, logger.Test(t), "", "testHash")

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
			Gateways: []mockGatewayEntry{{ID: "gateway1"}},
			OnSend:   func(id string) { readyCh <- id },
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

// TestGatewayOutboundProxy_SendRequest_Mtls verifies the cap http.MtlsAuth is converted and
// passed through to the outgoing gateway OutboundHTTPRequest.
func TestGatewayOutboundProxy_SendRequest_Mtls(t *testing.T) {
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}

	captureOutgoingRequest := func(t *testing.T, input *http.Request) *gateway_common.OutboundHTTPRequest {
		capturedCh := make(chan *gateway_common.OutboundHTTPRequest, 1)
		readyCh := make(chan string, 1)
		mockConnector := &mockGatewayConnector{
			Gateways: []mockGatewayEntry{{ID: "gateway1"}},
			OnSend:   func(id string) { readyCh <- id },
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

	t.Run("mTLS auth is passed through to gateway request", func(t *testing.T) {
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
			Mtls: &http.MtlsAuth{
				PrivateKey:  []byte("private-key"),
				Certificate: []byte("certificate"),
			},
		}
		req := captureOutgoingRequest(t, input)
		require.NotNil(t, req.Mtls)
		require.Equal(t, gateway_common.Secret("private-key"), req.Mtls.PrivateKey)
		require.Equal(t, []byte("certificate"), req.Mtls.Certificate)
	})

	t.Run("no mTLS auth leaves gateway request Mtls nil", func(t *testing.T) {
		input := &http.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		req := captureOutgoingRequest(t, input)
		require.Nil(t, req.Mtls)
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

	t.Run("valid UTF-8 preserved untouched (MultiHeaders source)", func(t *testing.T) {
		resp := &gateway_common.OutboundHTTPResponse{
			MultiHeaders: map[string][]string{"X-Multi": {"héllo", "日本語"}},
		}
		headers, multiHeaders := responseHeadersFromGateway(resp)
		require.Equal(t, []string{"héllo", "日本語"}, multiHeaders["X-Multi"].Values)
		require.Equal(t, "héllo,日本語", headers["X-Multi"]) //nolint:staticcheck // Headers deprecated
	})

	t.Run("invalid UTF-8 sanitized and marshalable (MultiHeaders source)", func(t *testing.T) {
		invalidVal := "x" + string([]byte{0xff, 0xfe})
		invalidKey := "X-Bad" + string([]byte{0xff})
		resp := &gateway_common.OutboundHTTPResponse{
			MultiHeaders: map[string][]string{invalidKey: {invalidVal, "clean"}},
		}
		_, multiHeaders := responseHeadersFromGateway(resp)
		key := common.SanitizeUTF8(invalidKey)
		require.Contains(t, multiHeaders, key)
		require.True(t, utf8.ValidString(multiHeaders[key].Values[0]))
		require.Equal(t, "clean", multiHeaders[key].Values[1])

		_, err := proto.Marshal(&http.Response{MultiHeaders: multiHeaders})
		require.NoError(t, err)
	})

	t.Run("invalid UTF-8 sanitized (Headers source)", func(t *testing.T) {
		invalidKey := "X-Bad" + string([]byte{0xff})
		resp := &gateway_common.OutboundHTTPResponse{
			Headers: map[string]string{invalidKey: "v" + string([]byte{0xfe})}, //nolint:staticcheck // Headers deprecated, testing
		}
		headers, multiHeaders := responseHeadersFromGateway(resp)
		key := common.SanitizeUTF8(invalidKey)
		require.Contains(t, multiHeaders, key)
		require.True(t, utf8.ValidString(headers[key])) //nolint:staticcheck // Headers deprecated
		require.True(t, utf8.ValidString(multiHeaders[key].Values[0]))
	})
}

func TestGatewayOutboundProxy_SendRequest_GatewayProxyDonIDRouting(t *testing.T) {
	t.Parallel()

	var resolvedDonID string
	readyCh := make(chan string, 1)

	mockConnector := &mockGatewayConnector{
		Gateways: []mockGatewayEntry{
			{ID: "gateway_eu", DonID: "gateway_don_eu"},
		},
		OnSend:    func(id string) { readyCh <- id },
		AwaitErrs: []error{nil},
	}

	baseValidator := newTestValidator(t)
	validator := &mockRequestValidator{
		RequestValidator: baseValidator,
		resolveDonID: func(context.Context) (string, error) {
			resolvedDonID = "gateway_don_eu"
			return resolvedDonID, nil
		},
	}

	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		common.ServiceConfig{},
		logger.Test(t),
		newMetrics(t),
		validator,
	)
	require.NoError(t, err)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Timeout:       testTimeout,
		CacheSettings: &http.CacheSettings{},
	}

	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 200, "ok", "", true)
	}()

	output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
	require.NoError(t, err)
	require.NotNil(t, output)
	require.Equal(t, "gateway_don_eu", resolvedDonID)
	require.Equal(t, []string{"gateway_eu"}, mockConnector.awaitCalls)
}

func TestGatewayOutboundProxy_SendRequest_emptyDonIDUsesAllGateways(t *testing.T) {
	t.Parallel()

	readyCh := make(chan string, 1)

	mockConnector := &mockGatewayConnector{
		Gateways: []mockGatewayEntry{
			{ID: "gateway_a"},
			{ID: "gateway_b"},
		},
		OnSend:    func(id string) { readyCh <- id },
		AwaitErrs: []error{nil},
	}

	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		common.ServiceConfig{},
		logger.Test(t),
		newMetrics(t),
		newTestValidator(t),
	)
	require.NoError(t, err)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &http.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Timeout:       testTimeout,
		CacheSettings: &http.CacheSettings{},
	}

	go func() {
		id := <-readyCh
		simulateGatewayMessage(t, proxy, id, 200, "ok", "", true)
	}()

	output, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
	require.NoError(t, err)
	require.NotNil(t, output)
	require.Len(t, mockConnector.awaitCalls, 1)
	require.Contains(t, []string{"gateway_a", "gateway_b"}, mockConnector.awaitCalls[0])
}

func TestGatewayOutboundProxy_gatewayIDsForDon_emptyDonID(t *testing.T) {
	t.Parallel()

	mockConnector := &mockGatewayConnector{
		Gateways: []mockGatewayEntry{
			{ID: "gateway_a"},
			{ID: "gateway_b"},
		},
	}
	proxy := &gatewayOutboundProxy{gatewayConnector: mockConnector}

	got, err := proxy.gatewayIDsForDon(t.Context(), "")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"gateway_a", "gateway_b"}, got)
}

func TestMockGatewayConnector_GatewayIDsForDon(t *testing.T) {
	t.Parallel()

	t.Run("legacy non-empty donID returns no gateways", func(t *testing.T) {
		t.Parallel()
		mockConnector := &mockGatewayConnector{
			Gateways: []mockGatewayEntry{
				{ID: "gateway_a"},
				{ID: "gateway_b"},
			},
		}
		got, err := mockConnector.GatewayIDsForDon(t.Context(), "gateway_don_eu")
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("multi-DON filters by per-gateway donID", func(t *testing.T) {
		t.Parallel()
		mockConnector := &mockGatewayConnector{
			Gateways: []mockGatewayEntry{
				{ID: "gateway_us_1", DonID: "gateway_don_us"},
				{ID: "gateway_us_2", DonID: "gateway_don_us"},
				{ID: "gateway_eu_1", DonID: "gateway_don_eu"},
			},
		}
		got, err := mockConnector.GatewayIDsForDon(t.Context(), "gateway_don_us")
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"gateway_us_1", "gateway_us_2"}, got)
	})
}

type mockRequestValidator struct {
	common.RequestValidator
	resolveDonID func(ctx context.Context) (string, error)
}

func (m *mockRequestValidator) ResolveGatewayProxyDonID(ctx context.Context) (string, error) {
	if m.resolveDonID != nil {
		return m.resolveDonID(ctx)
	}
	return m.RequestValidator.ResolveGatewayProxyDonID(ctx)
}
