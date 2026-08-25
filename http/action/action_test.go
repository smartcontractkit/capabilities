package action

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/smartcontractkit/capabilities/http/validate"

	"github.com/smartcontractkit/capabilities/http/protos"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	gcmocks "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
)

const WorkflowID = "workflow123"
const WorkflowExecutionID = "execution123"
const WorkflowOwner = "owner123"

type MockOutboundRequestClient struct {
	CapturedInput *protos.Request
	Response      *protos.Response
	Err           error
}

func (m *MockOutboundRequestClient) Start(ctx context.Context) error {
	return nil
}

func (m *MockOutboundRequestClient) Close() error {
	return nil
}

func (m *MockOutboundRequestClient) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *protos.Request, startTime time.Time) (*protos.Response, time.Duration, error) {
	m.CapturedInput = input
	return m.Response, 0, m.Err
}

func (m *MockOutboundRequestClient) HealthReport() map[string]error {
	return map[string]error{"MockOutboundRequestClient": nil}
}

func (m *MockOutboundRequestClient) Name() string {
	return "MockOutboundRequestClient"
}

func (m *MockOutboundRequestClient) Ready() error {
	return nil
}

// testSetup contains the test setup for service validation tests
type testSetup struct {
	service    *service
	mockClient *MockOutboundRequestClient
	metadata   capabilities.RequestMetadata
}

// setupServiceTest creates a fresh test setup for service validation tests
func setupServiceTest(t *testing.T) *testSetup {
	lggr := logger.Test(t)

	gc := gcmocks.NewGatewayConnector(t)
	// Registered when the capability starts rather than when it is built, and these
	// tests build one to look at it.
	gc.EXPECT().AddHandler(mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	srv, err := NewService(lggr, common.ServiceConfig{ProxyMode: common.ProxyModeGateway}, Dependencies{
		Connector:     gc,
		LimitsFactory: limits.Factory{},
	})
	require.NoError(t, err)

	mockClient := &MockOutboundRequestClient{}
	srv.client = mockClient
	srv.cfg = common.ServiceConfig{}

	limitsFactory := limits.Factory{
		Logger: logger.Test(t),
	}
	srv.limitsFactory = limitsFactory

	srv.validator, err = validate.NewValidator(logger.Test(t), limitsFactory)
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
		expectedResponse := &protos.Response{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = expectedResponse
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response.Response)
		assert.Equal(t, input, setup.mockClient.CapturedInput)
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
		expectedResponse := &protos.Response{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = expectedResponse
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response.Response)
		assert.Equal(t, input, setup.mockClient.CapturedInput)
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
		expectedResponse := &protos.Response{
			StatusCode: 200,
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = expectedResponse
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response.Response)
		assert.Equal(t, input, setup.mockClient.CapturedInput)
	})

	t.Run("invalid URL returns validation error from client", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:     "",
			Method:  "GET",
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		// Mock simulates client validating and returning error (real gateway/direct proxy does this)
		setup.mockClient.Err = common.InputValidationError{Err: errors.New("URL must not be empty")}

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "URL must not be empty")
		// Client is called; validation error is returned from client
		assert.NotNil(t, setup.mockClient.CapturedInput)
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
		expectedResponse := &protos.Response{
			StatusCode: 200,
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = expectedResponse
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response.Response)
		assert.Equal(t, input, setup.mockClient.CapturedInput)
	})

	t.Run("invalid HTTP method returns validation error from client", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &protos.Request{
			Url:     "https://example.com",
			Method:  "CONNECT",
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		setup.mockClient.Err = common.InputValidationError{Err: errors.New("invalid HTTP method: CONNECT")}

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "invalid HTTP method")
		assert.NotNil(t, setup.mockClient.CapturedInput)
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
		expectedResponse := &protos.Response{
			StatusCode: 200,
			Body:       []byte(`{"result": "success"}`),
		}
		setup.mockClient.Response = expectedResponse
		setup.mockClient.Err = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response.Response)
		assert.Equal(t, input, setup.mockClient.CapturedInput)
	})
}

// TestNewService_Rejects covers what a capability cannot be built with.
//
// It used to be about parsing a JSON config, because the node handed one over as
// a string. The configuration is decoded before this now, so what is left is what
// the values themselves have to be.
func TestNewService_Rejects(t *testing.T) {
	t.Run("a proxy mode that is not one", func(t *testing.T) {
		_, err := NewService(logger.Test(t), common.ServiceConfig{ProxyMode: common.ProxyMode(9)}, Dependencies{
			Connector:     gcmocks.NewGatewayConnector(t),
			LimitsFactory: limits.Factory{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid ProxyMode")
	})

	t.Run("gateway mode with no gateway to send through", func(t *testing.T) {
		_, err := NewService(logger.Test(t), common.ServiceConfig{ProxyMode: common.ProxyModeGateway}, Dependencies{
			LimitsFactory: limits.Factory{},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "gateway connector")
	})

	t.Run("direct mode needs no gateway: making the request itself is what it is", func(t *testing.T) {
		_, err := NewService(logger.Test(t), common.ServiceConfig{ProxyMode: common.ProxyModeDirect}, Dependencies{
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

		userError := NewUserError(errors.New("external endpoint failed"))
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
