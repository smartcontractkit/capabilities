package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/smartcontractkit/capabilities/http/validate"

	"github.com/smartcontractkit/capabilities/http/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
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
				gateway: mockConnector,
				metrics: newMetrics(t),
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
func setupSendRequestTest(t *testing.T) (*gatewayOutboundProxy, *mockGatewayConnector) {
	mockConnector := &mockGatewayConnector{
		SourceDonID: "don1",
		Gateways: []mockGatewayEntry{
			{ID: "gateway1"},
		},
	}
	lggr := logger.Test(t)
	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		common.GatewayConnectionConfig{},
		lggr,
		testLimits(t),
	)
	require.NoError(t, err)
	return proxy, mockConnector
}

func newMetrics(t *testing.T) *common.Metrics {
	m, err := common.NewMetrics()
	require.NoError(t, err)
	return m
}

func TestGatewayOutboundProxy_SendRequest_Success(t *testing.T) {
	proxy, _ := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &protos.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
		Body:          []byte("test"),
		Timeout:       testTimeout,
		CacheSettings: &protos.CacheSettings{Store: true},
	}

	output, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
	require.NoError(t, err)
	require.NotNil(t, output)
	assert.Equal(t, 200, output.StatusCode)
	assert.Equal(t, []byte("ok"), output.Body)
}

func TestGatewayOutboundProxy_SendRequest_MissingBodyToGateway(t *testing.T) {
	proxy, mockConnector := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &protos.Request{
		Url:     "http://example.com",
		Method:  "GET",
		Headers: map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
		Body:    []byte("test"),
		Timeout: testTimeout,
		CacheSettings: &protos.CacheSettings{
			Store:  true,
			MaxAge: durationpb.New(10 * time.Second), // 10 seconds
		},
	}

	// An answer with nothing in it is not an answer, and is refused rather than
	// read as an empty response.
	mockConnector.Answer = answering(gateway_common.OutboundHTTPResponse{StatusCode: 200}, false)

	_, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
	require.Error(t, err)
}

func TestGatewayOutboundProxy_SendRequest_ExecutionError(t *testing.T) {
	proxy, mockConnector := setupSendRequestTest(t)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &protos.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
		Body:          []byte("test"),
		Timeout:       testTimeout,
		CacheSettings: &protos.CacheSettings{Store: true},
	}

	mockConnector.Answer = answering(gateway_common.OutboundHTTPResponse{
		StatusCode:   500,
		Body:         []byte("ok"),
		ErrorMessage: "some error",
	}, true)

	_, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
	require.Error(t, err)
	var userErr common.UserError
	assert.False(t, errors.As(err, &userErr))
	assert.Contains(t, err.Error(), "gateway returned error")
}

func TestGatewayOutboundProxy_SendRequest_UserErrors(t *testing.T) {
	t.Run("external endpoint error returns UserError", func(t *testing.T) {
		proxy, mockConnector := setupSendRequestTest(t)

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
			Body:          []byte("test"),
			Timeout:       testTimeout,
			CacheSettings: &protos.CacheSettings{Store: true},
		}

		mockConnector.Answer = answering(gateway_common.OutboundHTTPResponse{
			StatusCode:              500,
			ErrorMessage:            "endpoint failed",
			IsExternalEndpointError: true,
		}, true)

		_, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
		require.Error(t, err)

		var userErr common.UserError
		assert.True(t, errors.As(err, &userErr))
		assert.Equal(t, "endpoint failed", err.Error())
	})

	t.Run("validation error returns UserError", func(t *testing.T) {
		proxy, mockConnector := setupSendRequestTest(t)

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "1"}, //nolint:staticcheck // Headers deprecated
			Body:          []byte("test"),
			Timeout:       testTimeout,
			CacheSettings: &protos.CacheSettings{Store: true},
		}

		mockConnector.Answer = answering(gateway_common.OutboundHTTPResponse{
			StatusCode:        400,
			ErrorMessage:      "invalid request format",
			IsValidationError: true,
		}, true)

		_, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
		require.Error(t, err)

		var userErr common.UserError
		assert.True(t, errors.As(err, &userErr))
		assert.Equal(t, "invalid request format", err.Error())
	})

	// Ensure that canceling the SendRequest context before a gateway response
	// is a UserError.
	t.Run("gateway response timeout returns UserError", func(t *testing.T) {
		proxy, mockConnector := setupSendRequestTest(t)

		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}
		input := &protos.Request{
			Url:    "http://example.com",
			Method: "GET",
			MultiHeaders: map[string]*protos.HeaderValues{
				"X-Test": {
					Values: []string{"1"},
				},
			},
			Body:          []byte("test"),
			Timeout:       testTimeout,
			CacheSettings: &protos.CacheSettings{Store: true},
		}

		// A gateway that is still fetching: the request is held open until the caller
		// gives up on it, which is what a workflow's timeout does.
		asked := make(chan struct{})
		mockConnector.Answer = func(ctx context.Context, _ *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error) {
			close(asked)
			<-ctx.Done()
			return nil, ctx.Err()
		}

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		errCh := make(chan error, 1)
		var output gateway_common.OutboundHTTPResponse
		go func() {
			var err error
			output, err = proxy.SendRequest(ctx, common.OutboundRequest(metadata, input))
			errCh <- err
		}()

		// cancel once the gateway has the request
		<-asked
		cancel()

		err := <-errCh
		require.Error(t, err)
		require.Zero(t, output.StatusCode)
		assert.Contains(t, err.Error(), ErrMsgGatewayResponseWait)
		assert.Contains(t, err.Error(), "context canceled")

		var userErr common.UserError
		assert.True(t, errors.As(err, &userErr))
	})
}

// answering is a gateway that hands back a response of its own: the same message
// it would once have pushed at the node, on the request the node asked with.
//
// withResponse says whether the answer carries one at all, which is how a gateway
// answering with nothing is tested.
func answering(resp gateway_common.OutboundHTTPResponse, withResponse bool) gatewayAnswer {
	return func(_ context.Context, asked *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error) {
		answer := &jsonrpc.Request[json.RawMessage]{
			ID:      asked.ID,
			Method:  gateway_common.MethodHTTPAction,
			Version: "2.0",
		}
		if withResponse {
			payload, err := json.Marshal(resp)
			if err != nil {
				return nil, err
			}
			params := json.RawMessage(payload)
			answer.Params = &params
		}
		return answer, nil
	}
}

type mockGatewayEntry struct {
	ID    string
	DonID string
}

type mockGatewayConnector struct {
	core.GatewayConnector
	// OnTunnel is what a tunnelled request gets. Nil refuses, which is what most of
	// these tests mean: their requests are cached ones, and a cached request the
	// gateway fetches never asks for a tunnel.
	OnTunnel      func(ctx context.Context, gatewayID, address string) (net.Conn, error)
	SourceDonID   string
	Gateways      []mockGatewayEntry
	SendErr       error
	AwaitErrs     []error
	AddHandlerErr error
	OnSend        func(id string)
	// CaptureSendPayload, if set, is called with the full response sent to the gateway (Result = marshalled OutboundHTTPRequest).
	CaptureSendPayload func(*jsonrpc.Response[json.RawMessage])
	// Answer is what this gateway answers a request with. Nil answers what an
	// ordinary fetch would: 200, with a body.
	Answer gatewayAnswer

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

// gatewayAnswer is a gateway's end of a request a node is waiting on.
type gatewayAnswer func(context.Context, *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error)

func (m *mockGatewayConnector) Request(ctx context.Context, gateway string, resp *jsonrpc.Response[json.RawMessage]) (*jsonrpc.Request[json.RawMessage], error) {
	if m.OnSend != nil {
		m.OnSend(gateway)
	}
	if m.CaptureSendPayload != nil {
		m.CaptureSendPayload(resp)
	}
	if m.SendErr != nil {
		return nil, m.SendErr
	}
	if m.Answer != nil {
		return m.Answer(ctx, resp)
	}
	return answering(gateway_common.OutboundHTTPResponse{StatusCode: 200, Body: []byte("ok")}, true)(ctx, resp)
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
			gateway: mockConnector,
			metrics: newMetrics(t),
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
	captureOutgoingRequest := func(t *testing.T, input *protos.Request) *gateway_common.OutboundHTTPRequest {
		capturedCh := make(chan *gateway_common.OutboundHTTPRequest, 1)
		mockConnector := &mockGatewayConnector{
			Gateways: []mockGatewayEntry{{ID: "gateway1"}},
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
		proxy, err := NewGatewayOutboundProxy(mockConnector, common.GatewayConnectionConfig{}, lggr, testLimits(t))
		require.NoError(t, err)
		_, err = proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
		require.NoError(t, err)
		req := <-capturedCh
		require.NotNil(t, req, "CaptureSendPayload should have been called")
		return req
	}

	// --- Outgoing request (cap → gateway) ---

	t.Run("outgoing: MultiHeaders only when input has MultiHeaders", func(t *testing.T) {
		input := &protos.Request{
			Url:    "http://example.com",
			Method: "GET",
			MultiHeaders: map[string]*protos.HeaderValues{
				"Accept":     {Values: []string{"application/json"}},
				"Set-Cookie": {Values: []string{"a=1", "b=2"}},
			},
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{Store: true},
		}
		req := captureOutgoingRequest(t, input)
		require.Len(t, req.MultiHeaders, 2)
		require.Equal(t, []string{"application/json"}, req.MultiHeaders["Accept"])
		require.Equal(t, []string{"a=1", "b=2"}, req.MultiHeaders["Set-Cookie"])
		require.Empty(t, req.Headers, "OutboundHTTPRequest must set only MultiHeaders when input has MultiHeaders") //nolint:staticcheck // Headers deprecated, testing exclusive MultiHeaders
	})

	t.Run("outgoing: Headers only when input has no MultiHeaders", func(t *testing.T) {
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Headers:       map[string]string{"X-Test": "value"}, //nolint:staticcheck // Headers deprecated
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{Store: true},
		}
		req := captureOutgoingRequest(t, input)
		require.Equal(t, map[string]string{"X-Test": "value"}, req.Headers) //nolint:staticcheck // Headers deprecated, testing exclusive Headers
		require.Empty(t, req.MultiHeaders, "OutboundHTTPRequest must set only Headers when input has no MultiHeaders")
	})

	t.Run("outgoing: neither set when input has no headers", func(t *testing.T) {
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{Store: true},
		}
		req := captureOutgoingRequest(t, input)
		require.Empty(t, req.Headers) //nolint:staticcheck // Headers deprecated
		require.Empty(t, req.MultiHeaders)
	})

	// --- Incoming response (gateway → cap) ---

	t.Run("incoming: MultiHeaders preserved and Headers comma-joined", func(t *testing.T) {
		proxy, mockConnector := setupSendRequestTest(t)
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{Store: true},
		}
		gatewayMultiHeaders := map[string][]string{
			"Set-Cookie": {
				"sessionid=abc123; Path=/; HttpOnly",
				"csrf_token=xyz789; Path=/; Secure",
				"pref=dark; Path=/",
			},
		}
		gatewayHeaders := map[string]string{"Set-Cookie": "sessionid=abc123; Path=/; HttpOnly"}

		mockConnector.Answer = answering(gateway_common.OutboundHTTPResponse{
			StatusCode:   200,
			Body:         []byte("ok"),
			Headers:      gatewayHeaders, //nolint:staticcheck // Headers deprecated, gateway may send
			MultiHeaders: gatewayMultiHeaders,
		}, true)

		output, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
		require.NoError(t, err)
		require.NotZero(t, output.StatusCode)
		require.Equal(t, gatewayMultiHeaders, output.MultiHeaders, "what the gateway answered is what comes back")
		require.Equal(t, gatewayHeaders, output.Headers)
	})

	t.Run("incoming: response always has both Headers and MultiHeaders; gateway sent only Headers", func(t *testing.T) {
		proxy, mockConnector := setupSendRequestTest(t)
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{Store: true},
		}
		gatewayHeaders := map[string]string{"Content-Type": "application/json"}

		mockConnector.Answer = answering(gateway_common.OutboundHTTPResponse{
			StatusCode: 200,
			Body:       []byte("ok"),
			Headers:    gatewayHeaders, //nolint:staticcheck // Headers deprecated, gateway may send
		}, true)

		output, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
		require.NoError(t, err)
		require.NotNil(t, output)
		require.Equal(t, gatewayHeaders, output.Headers, "what the gateway answered is what comes back, whichever form it used")
		require.Empty(t, output.MultiHeaders, "nothing here invents the other form; the capability derives it")
	})
}

// TestGatewayOutboundProxy_SendRequest_Mtls verifies the cap protos.MtlsAuth is converted and
// passed through to the outgoing gateway OutboundHTTPRequest.
func TestGatewayOutboundProxy_SendRequest_Mtls(t *testing.T) {
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}

	captureOutgoingRequest := func(t *testing.T, input *protos.Request) *gateway_common.OutboundHTTPRequest {
		capturedCh := make(chan *gateway_common.OutboundHTTPRequest, 1)
		mockConnector := &mockGatewayConnector{
			Gateways: []mockGatewayEntry{{ID: "gateway1"}},
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
		proxy, err := NewGatewayOutboundProxy(mockConnector, common.GatewayConnectionConfig{}, lggr, testLimits(t))
		require.NoError(t, err)
		_, err = proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
		require.NoError(t, err)
		req := <-capturedCh
		require.NotNil(t, req, "CaptureSendPayload should have been called")
		return req
	}

	t.Run("mTLS auth is passed through to gateway request", func(t *testing.T) {
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{Store: true},
			Mtls: &protos.MtlsAuth{
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
		input := &protos.Request{
			Url:           "http://example.com",
			Method:        "GET",
			Body:          []byte{},
			Timeout:       durationpb.New(5000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{Store: true},
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

		_, err := proto.Marshal(&protos.Response{MultiHeaders: multiHeaders})
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

	mockConnector := &mockGatewayConnector{
		Gateways: []mockGatewayEntry{
			{ID: "gateway_eu", DonID: "gateway_don_eu"},
		},
		AwaitErrs: []error{nil},
	}

	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		common.GatewayConnectionConfig{},
		logger.Test(t),
		limitsWithGatewayDon(t, "gateway_don_eu"),
	)
	require.NoError(t, err)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &protos.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Timeout:       testTimeout,
		CacheSettings: &protos.CacheSettings{Store: true},
	}

	// The settings are per-workflow, so which DON they name is resolved for the
	// workflow the request is on behalf of - which is what the context carries.
	ctx := contexts.WithCRE(t.Context(), contexts.CRE{Org: "test-org", Owner: "owner1", Workflow: "wf1"})

	output, err := proxy.SendRequest(ctx, common.OutboundRequest(metadata, input))
	require.NoError(t, err)
	require.NotZero(t, output.StatusCode)
	require.Equal(t, []string{"gateway_eu"}, mockConnector.awaitCalls, "the DON the settings name is the DON it is sent to")
}

func TestGatewayOutboundProxy_SendRequest_emptyDonIDUsesAllGateways(t *testing.T) {
	t.Parallel()

	mockConnector := &mockGatewayConnector{
		Gateways: []mockGatewayEntry{
			{ID: "gateway_a"},
			{ID: "gateway_b"},
		},
		AwaitErrs: []error{nil},
	}

	proxy, err := NewGatewayOutboundProxy(
		mockConnector,
		common.GatewayConnectionConfig{},
		logger.Test(t),
		testLimits(t),
	)
	require.NoError(t, err)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	input := &protos.Request{
		Url:           "http://example.com",
		Method:        "GET",
		Timeout:       testTimeout,
		CacheSettings: &protos.CacheSettings{Store: true},
	}

	output, err := proxy.SendRequest(t.Context(), common.OutboundRequest(metadata, input))
	require.NoError(t, err)
	require.NotZero(t, output.StatusCode)
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
	proxy := &gatewayOutboundProxy{gateway: mockConnector}

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

// Tunnel is the connection in its other role. A test that means its request to be
// tunnelled says so by setting OnTunnel; one that does not is told, rather than
// left to wonder why the far side never heard from it.
func (m *mockGatewayConnector) Tunnel(ctx context.Context, gatewayID, address string) (net.Conn, error) {
	if m.OnTunnel == nil {
		return nil, errors.New("this test's requests go through the gateway, not through a tunnel")
	}
	return m.OnTunnel(ctx, gatewayID, address)
}

// testLimits is the settings a gateway proxy reads the DON to send to from.
func testLimits(t *testing.T) limits.Factory {
	return limits.Factory{Logger: logger.Test(t)}
}
