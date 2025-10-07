package action

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http_action/common"
	"github.com/smartcontractkit/capabilities/http_action/validate"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	gcmocks "github.com/smartcontractkit/chainlink-common/pkg/types/core/mocks"
)

const WorkflowID = "workflow123"
const WorkflowExecutionID = "execution123"
const WorkflowOwner = "owner123"

type MockOutboundRequestClient struct {
	CapturedInput *http.Request
	Response      *http.Response
	Err           error
}

func (m *MockOutboundRequestClient) Start(ctx context.Context) error {
	return nil
}

func (m *MockOutboundRequestClient) Close() error {
	return nil
}

func (m *MockOutboundRequestClient) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request, startTime time.Time) (*http.Response, error) {
	m.CapturedInput = input
	return m.Response, m.Err
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
	srv := NewService(lggr, limits.Factory{})
	cfg := common.ServiceConfig{
		ProxyMode: common.ProxyModeGateway,
	}
	cfgStr, err := json.Marshal(cfg)
	require.NoError(t, err)
	gc := gcmocks.NewGatewayConnector(t)
	gc.EXPECT().AddHandler(mock.Anything, mock.Anything, mock.Anything).Return(nil)
	err = srv.Initialise(t.Context(), string(cfgStr), nil, nil, nil, nil, nil, nil, gc, nil)
	require.NoError(t, err)

	mockClient := &MockOutboundRequestClient{}
	srv.client = mockClient
	srv.cfg = common.ServiceConfig{}

	limitsFactory := limits.Factory{
		Logger: logger.Test(t),
	}
	srv.limitsFactory = limitsFactory

	srv.rateLimiter, err = limitsFactory.MakeRateLimiter(cresettings.Default.PerWorkflow.HTTPAction.RateLimit)
	require.NoError(t, err)

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

		input := &http.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Headers:       map[string]string{"Content-Type": "application/json"},
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		expectedResponse := &http.Response{
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

		input := &http.Request{
			Url:     "https://example.com",
			Method:  "GET",
			Headers: map[string]string{"Content-Type": "application/json"},
			Timeout: durationpb.New(1000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{
				Store:  true,
				MaxAge: durationpb.New(10 * time.Second), // 10 seconds
			},
		}
		expectedResponse := &http.Response{
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

		input := &http.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Headers:       nil,
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		expectedResponse := &http.Response{
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

	t.Run("invalid URL fails validation and doesn't call client", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &http.Request{
			Url:     "",
			Method:  "GET",
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		setup.mockClient.CapturedInput = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "URL must not be empty")
		assert.Nil(t, setup.mockClient.CapturedInput)
	})

	t.Run("request with large body gets processed", func(t *testing.T) {
		setup := setupServiceTest(t)

		allowedSize := cresettings.Default.PerWorkflow.HTTPAction.RequestSizeLimit.DefaultValue / 2
		largeBody := make([]byte, allowedSize)
		input := &http.Request{
			Url:           "https://example.com",
			Method:        "POST",
			Body:          largeBody,
			Timeout:       durationpb.New(1000 * time.Millisecond),
			CacheSettings: &http.CacheSettings{},
		}
		expectedResponse := &http.Response{
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

	t.Run("invalid HTTP method fails validation", func(t *testing.T) {
		setup := setupServiceTest(t)

		input := &http.Request{
			Url:     "https://example.com",
			Method:  "CONNECT",
			Timeout: durationpb.New(1000 * time.Millisecond),
		}
		setup.mockClient.CapturedInput = nil

		response, err := setup.service.SendRequest(t.Context(), setup.metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "invalid HTTP method")
		assert.Nil(t, setup.mockClient.CapturedInput)
	})

	t.Run("request with normal timeout gets processed", func(t *testing.T) {
		setup := setupServiceTest(t)

		allowedTimeout := cresettings.Default.PerWorkflow.HTTPAction.ConnectionTimeout.DefaultValue / 2
		input := &http.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Timeout:       durationpb.New(allowedTimeout),
			CacheSettings: &http.CacheSettings{},
		}
		expectedResponse := &http.Response{
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

func TestInitialise_NilConfig(t *testing.T) {
	t.Run("empty config string should return error", func(t *testing.T) {
		lggr := logger.Test(t)
		srv := NewService(lggr, limits.Factory{})
		gc := gcmocks.NewGatewayConnector(t)

		err := srv.Initialise(context.Background(), "", nil, nil, nil, nil, nil, nil, gc, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected end of JSON input")
	})

	t.Run("invalid JSON config should return error", func(t *testing.T) {
		lggr := logger.Test(t)
		srv := NewService(lggr, limits.Factory{})
		gc := gcmocks.NewGatewayConnector(t)

		err := srv.Initialise(context.Background(), "invalid json", nil, nil, nil, nil, nil, nil, gc, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid character")
	})

	t.Run("empty object config should return error due to missing proxyMode", func(t *testing.T) {
		lggr := logger.Test(t)
		srv := NewService(lggr, limits.Factory{})
		gc := gcmocks.NewGatewayConnector(t)

		err := srv.Initialise(context.Background(), "{}", nil, nil, nil, nil, nil, nil, gc, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid proxy mode")
	})

	t.Run("null config string should return error due to missing proxyMode", func(t *testing.T) {
		lggr := logger.Test(t)
		srv := NewService(lggr, limits.Factory{})
		gc := gcmocks.NewGatewayConnector(t)

		err := srv.Initialise(context.Background(), "null", nil, nil, nil, nil, nil, nil, gc, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid proxy mode")
	})
}
