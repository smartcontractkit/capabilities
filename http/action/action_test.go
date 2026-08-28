package action

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http/common"

	"github.com/smartcontractkit/capabilities/http/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	gc "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"
)

const WorkflowID = "workflow123"
const WorkflowExecutionID = "execution123"
const WorkflowOwner = "owner123"

// MockOutbound stands in for wherever requests go. It is the whole of what the
// capability is given, which is what these tests are about: everything else -
// the limits, the validation, the errors a workflow is answered with - is the
// capability's own, and is exercised without a gateway anywhere in sight.
type MockOutbound struct {
	CapturedInput gc.OutboundHTTPRequest
	Response      gc.OutboundHTTPResponse
	Err           error
}

var _ common.Outbound = (*MockOutbound)(nil)

func (m *MockOutbound) Start(context.Context) error { return nil }

func (m *MockOutbound) Close() error { return nil }

func (m *MockOutbound) SendRequest(_ context.Context, request gc.OutboundHTTPRequest) (gc.OutboundHTTPResponse, error) {
	m.CapturedInput = request
	return m.Response, m.Err
}

func (m *MockOutbound) HealthReport() map[string]error {
	return map[string]error{m.Name(): nil}
}

func (m *MockOutbound) Name() string { return "MockOutbound" }

func (m *MockOutbound) Ready() error { return nil }

// testSetup contains the test setup for service validation tests
type testSetup struct {
	service    *service
	mockClient *MockOutbound
	metadata   capabilities.RequestMetadata
}

// setupServiceTest creates a fresh test setup for service validation tests
func setupServiceTest(t *testing.T) *testSetup {
	limitsFactory := limits.Factory{Logger: logger.Test(t)}

	mockClient := &MockOutbound{}
	srv, err := NewService(logger.Test(t), Dependencies{
		Outbound:      mockClient,
		LimitsFactory: limitsFactory,
	})
	require.NoError(t, err)

	metadata := capabilities.RequestMetadata{
		WorkflowID:          WorkflowID,
		WorkflowExecutionID: WorkflowExecutionID,
		WorkflowOwner:       WorkflowOwner,
	}
	return &testSetup{
		service:    srv,
		mockClient: mockClient,
		metadata:   metadata,
	}
}

func TestSendRequest_ValidatesInput(t *testing.T) {
	t.Run("valid request gets validated and forwarded to client", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Headers:       map[string]string{"Content-Type": "application/json"},
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{},
		}
		answered := gc.OutboundHTTPResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = answered
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, uint32(answered.StatusCode), response.Response.StatusCode)
		assert.Equal(t, answered.Body, response.Response.Body)
		assert.Equal(t, answered.Headers["Content-Type"], response.Response.Headers["Content-Type"]) //nolint:staticcheck // Headers is what these tests set
		assert.Equal(t, input.Url, setup.mockClient.CapturedInput.URL)
		assert.Equal(t, input.Method, setup.mockClient.CapturedInput.Method)
	})

	t.Run("valid request with cache settings gets validated and forwarded to client", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:     "https://example.com",
			Method:  "GET",
			Headers: map[string]string{"Content-Type": "application/json"},
			Timeout: durationpb.New(1000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{
				Store:  true,
				MaxAge: durationpb.New(10 * time.Second), // 10 seconds
			},
		}
		answered := gc.OutboundHTTPResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = answered
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, uint32(answered.StatusCode), response.Response.StatusCode)
		assert.Equal(t, answered.Body, response.Response.Body)
		assert.Equal(t, answered.Headers["Content-Type"], response.Response.Headers["Content-Type"]) //nolint:staticcheck // Headers is what these tests set
		assert.Equal(t, input.Url, setup.mockClient.CapturedInput.URL)
		assert.Equal(t, input.Method, setup.mockClient.CapturedInput.Method)
	})

	t.Run("empty headers are allowed", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Headers:       nil,
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{},
		}
		answered := gc.OutboundHTTPResponse{
			StatusCode: 200,
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = answered
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, uint32(answered.StatusCode), response.Response.StatusCode)
		assert.Equal(t, answered.Body, response.Response.Body)
		assert.Equal(t, answered.Headers["Content-Type"], response.Response.Headers["Content-Type"]) //nolint:staticcheck // Headers is what these tests set
		assert.Equal(t, input.Url, setup.mockClient.CapturedInput.URL)
		assert.Equal(t, input.Method, setup.mockClient.CapturedInput.Method)
	})

	t.Run("invalid URL is refused before anything is sent", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:     "",
			Method:  "GET",
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "URL must not be empty")
		// Validation is the capability's, and it happens first: nothing was sent
		// anywhere, whatever "anywhere" would have been.
		assert.Empty(t, setup.mockClient.CapturedInput.URL)
	})

	t.Run("request with large body gets processed", func(t *testing.T) {
		setup := setupServiceTest(t)

		allowedSize := cresettings.Default.PerWorkflow.HTTPAction.RequestSizeLimit.DefaultValue / 2
		largeBody := make([]byte, allowedSize)
		input := &protos.Request{
			Url:           "https://example.com",
			Method:        "POST",
			Body:          largeBody,
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{},
		}
		answered := gc.OutboundHTTPResponse{
			StatusCode: 200,
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = answered
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, uint32(answered.StatusCode), response.Response.StatusCode)
		assert.Equal(t, answered.Body, response.Response.Body)
		assert.Equal(t, answered.Headers["Content-Type"], response.Response.Headers["Content-Type"]) //nolint:staticcheck // Headers is what these tests set
		assert.Equal(t, input.Url, setup.mockClient.CapturedInput.URL)
		assert.Equal(t, input.Method, setup.mockClient.CapturedInput.Method)
	})

	t.Run("invalid HTTP method is refused before anything is sent", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:     "https://example.com",
			Method:  "CONNECT",
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "method")
		assert.Empty(t, setup.mockClient.CapturedInput.URL, "nothing should have been sent")
	})

	t.Run("request with normal timeout gets processed", func(t *testing.T) {
		setup := setupServiceTest(t)

		allowedTimeout := cresettings.Default.PerWorkflow.HTTPAction.ConnectionTimeout.DefaultValue / 2
		input := &protos.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Timeout:       durationpb.New(allowedTimeout),
			CacheSettings: &protos.CacheSettings{},
		}
		answered := gc.OutboundHTTPResponse{
			StatusCode: 200,
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = answered
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, uint32(answered.StatusCode), response.Response.StatusCode)
		assert.Equal(t, answered.Body, response.Response.Body)
		assert.Equal(t, answered.Headers["Content-Type"], response.Response.Headers["Content-Type"]) //nolint:staticcheck // Headers is what these tests set
		assert.Equal(t, input.Url, setup.mockClient.CapturedInput.URL)
		assert.Equal(t, input.Method, setup.mockClient.CapturedInput.Method)
	})
}

// TestNewService_Rejects covers what a capability cannot be built with.
//
// Which is now one thing. It used to be about proxy modes: whether the mode was
// one, whether a gateway had been supplied for it. None of that reaches here any
// more - where requests go is settled before this is built, and what arrives is
// something that makes them.
func TestNewService_Rejects(t *testing.T) {
	t.Run("nowhere for its requests to go", func(t *testing.T) {
		_, err := NewService(logger.Test(t), Dependencies{LimitsFactory: limits.Factory{}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "somewhere for its requests to go")
	})

	t.Run("anything that makes requests will do", func(t *testing.T) {
		_, err := NewService(logger.Test(t), Dependencies{
			Outbound:      &MockOutbound{},
			LimitsFactory: limits.Factory{},
		})
		require.NoError(t, err)
	})
}

func TestSendRequest_ErrorHandling(t *testing.T) {
	t.Run("client returns limit validation error with LimitExceeded code", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{},
		}

		limitErr := limits.ErrorBoundLimited[config.Size]{Key: "RequestSizeLimit", Limit: 1, Amount: 2}
		setup.mockClient.Err = common.InputValidationError{Err: limitErr}

		_, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)

		var capErr caperrors.Error
		assert.True(t, errors.As(err, &capErr))
		assert.Equal(t, caperrors.LimitExceeded, capErr.Code())
	})

	t.Run("client returns UserError and service returns PublicUserError", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{},
		}

		userError := common.NewUserError(errors.New("external endpoint failed"))
		setup.mockClient.Err = userError

		_, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)

		var capErr caperrors.Error
		assert.True(t, errors.As(err, &capErr))
		assert.Equal(t, caperrors.InvalidArgument, capErr.Code())
		assert.Equal(t, caperrors.VisibilityPublic, capErr.Visibility())
	})

	t.Run("client returns system error and service returns PublicSystemError", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &protos.CacheSettings{},
		}

		systemError := errors.New("internal system error")
		setup.mockClient.Err = systemError

		_, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)

		var capErr caperrors.Error
		assert.True(t, errors.As(err, &capErr))
		assert.Equal(t, caperrors.Internal, capErr.Code())
		assert.Equal(t, caperrors.VisibilityPublic, capErr.Visibility())
	})
}

// The limits are the capability's, applied to whatever answered: how much a
// workflow may be given back does not depend on where the request went.
func TestResponseSizeIsTheCapabilitysLimit(t *testing.T) {
	setup := setupServiceTest(t)
	setup.mockClient.Response = gc.OutboundHTTPResponse{
		StatusCode: 200,
		Body:       make([]byte, 10*1024*1024),
	}

	_, err := setup.service.SendRequest(t.Context(), setup.metadata, &protos.Request{
		Url:           "https://example.com",
		Method:        "GET",
		Timeout:       durationpb.New(time.Second),
		CacheSettings: &protos.CacheSettings{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ResponseSizeLimit limited")
}

// Validation is the capability's too, and it runs before anything is sent: a
// request that names both header forms means two things at once, and is refused
// here rather than by whoever would have carried it.
func TestBothHeaderFormsAreRefused(t *testing.T) {
	setup := setupServiceTest(t)

	_, err := setup.service.SendRequest(t.Context(), setup.metadata, &protos.Request{
		Url:           "https://example.com",
		Method:        "GET",
		Headers:       map[string]string{"X-Test": "value"}, //nolint:staticcheck // Headers is deprecated but is what this refuses alongside MultiHeaders
		MultiHeaders:  map[string]*protos.HeaderValues{"Accept": {Values: []string{"application/json"}}},
		Timeout:       durationpb.New(time.Second),
		CacheSettings: &protos.CacheSettings{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "either Headers or MultiHeaders, not both")
	assert.Empty(t, setup.mockClient.CapturedInput.URL, "nothing should have been sent")
}

// A workflow reads either header form, so both are filled in whichever the
// answer used.
func TestResponseHeadersAreGivenBothWays(t *testing.T) {
	t.Run("from the repeated form", func(t *testing.T) {
		setup := setupServiceTest(t)
		setup.mockClient.Response = gc.OutboundHTTPResponse{
			StatusCode: 200,
			MultiHeaders: map[string][]string{
				"Set-Cookie": {"sessionid=abc123; Path=/", "pref=dark; Path=/"},
			},
		}

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, &protos.Request{
			Url: "https://example.com", Method: "GET",
			Timeout: durationpb.New(time.Second), CacheSettings: &protos.CacheSettings{},
		})
		require.NoError(t, err)

		assert.Equal(t, []string{"sessionid=abc123; Path=/", "pref=dark; Path=/"}, response.Response.MultiHeaders["Set-Cookie"].Values)
		//nolint:staticcheck // Headers is deprecated, and is derived for whoever still reads it
		assert.Equal(t, "sessionid=abc123; Path=/,pref=dark; Path=/", response.Response.Headers["Set-Cookie"])
	})

	t.Run("from the single form", func(t *testing.T) {
		setup := setupServiceTest(t)
		setup.mockClient.Response = gc.OutboundHTTPResponse{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
		}

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, &protos.Request{
			Url: "https://example.com", Method: "GET",
			Timeout: durationpb.New(time.Second), CacheSettings: &protos.CacheSettings{},
		})
		require.NoError(t, err)

		//nolint:staticcheck // Headers is deprecated but is what the answer set
		assert.Equal(t, "application/json", response.Response.Headers["Content-Type"])
		assert.Equal(t, []string{"application/json"}, response.Response.MultiHeaders["Content-Type"].Values)
	})
}
