package action

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/http_action/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

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

func (m *MockOutboundRequestClient) SendRequest(ctx context.Context, metadata capabilities.RequestMetadata, input *http.Request) (*http.Response, error) {
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

func TestSendRequest_ValidatesInput(t *testing.T) {
	lggr := logger.Test(t)
	srv := NewService(lggr)
	mockClient := &MockOutboundRequestClient{}
	srv.client = mockClient
	srv.cfg = common.ServiceConfig{
		LimitsConfig: common.LimitsConfig{
			MaxTimeoutMs:         5000,
			MaxHeaderCount:       10,
			MaxHeaderKeyLength:   50,
			MaxHeaderValueLength: 100,
			MaxRequestBytes:      1024,
		},
	}
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "workflow123",
		WorkflowExecutionID: "execution123",
		WorkflowOwner:       "owner123",
	}

	t.Run("valid request gets validated and forwarded to client", func(t *testing.T) {
		input := &http.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Headers:       map[string]string{"Content-Type": "application/json"},
			TimeoutMs:     1000,
			CacheSettings: &http.CacheSettings{},
		}
		expectedResponse := &http.Response{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       []byte(`{"result": "success"}`),
		}
		mockClient.Response = expectedResponse
		mockClient.Err = nil

		response, err := srv.SendRequest(context.Background(), metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response)
		assert.Equal(t, input, mockClient.CapturedInput)
	})

	t.Run("valid request with cache settings gets validated and forwarded to client", func(t *testing.T) {
		input := &http.Request{
			Url:       "https://example.com",
			Method:    "GET",
			Headers:   map[string]string{"Content-Type": "application/json"},
			TimeoutMs: 1000,
			CacheSettings: &http.CacheSettings{
				ReadFromCache: true,
				MaxAgeMs:      10000, // 10 seconds
			},
		}
		expectedResponse := &http.Response{
			StatusCode: 200,
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       []byte(`{"result": "success"}`),
		}
		mockClient.Response = expectedResponse
		mockClient.Err = nil

		response, err := srv.SendRequest(context.Background(), metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response)
		assert.Equal(t, input, mockClient.CapturedInput)
	})

	t.Run("empty headers are allowed", func(t *testing.T) {
		input := &http.Request{
			Url:           "https://example.com",
			Method:        "GET",
			Headers:       nil,
			TimeoutMs:     1000,
			CacheSettings: &http.CacheSettings{},
		}
		expectedResponse := &http.Response{
			StatusCode: 200,
			Body:       []byte(`{"result": "success"}`),
		}
		mockClient.Response = expectedResponse
		mockClient.Err = nil

		response, err := srv.SendRequest(context.Background(), metadata, input)
		require.NoError(t, err)
		assert.Equal(t, expectedResponse, response)
		assert.Equal(t, input, mockClient.CapturedInput)
	})

	t.Run("invalid URL fails validation and doesn't call client", func(t *testing.T) {
		input := &http.Request{
			Url:       "",
			Method:    "GET",
			TimeoutMs: 1000,
		}
		mockClient.CapturedInput = nil

		response, err := srv.SendRequest(context.Background(), metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "URL must not be empty")
		assert.Nil(t, mockClient.CapturedInput)
	})

	t.Run("request with body exceeding limit fails validation", func(t *testing.T) {
		largeBody := make([]byte, 1025)
		input := &http.Request{
			Url:       "https://example.com",
			Method:    "POST",
			Body:      largeBody,
			TimeoutMs: 1000,
		}
		mockClient.CapturedInput = nil

		response, err := srv.SendRequest(context.Background(), metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "body too large")
		assert.Nil(t, mockClient.CapturedInput)
	})

	t.Run("invalid HTTP method fails validation", func(t *testing.T) {
		input := &http.Request{
			Url:       "https://example.com",
			Method:    "CONNECT",
			TimeoutMs: 1000,
		}
		mockClient.CapturedInput = nil

		response, err := srv.SendRequest(context.Background(), metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "invalid HTTP method")
		assert.Nil(t, mockClient.CapturedInput)
	})

	t.Run("timeout exceeding limit fails validation", func(t *testing.T) {
		input := &http.Request{
			Url:       "https://example.com",
			Method:    "GET",
			TimeoutMs: 10000,
		}
		mockClient.CapturedInput = nil

		response, err := srv.SendRequest(context.Background(), metadata, input)
		require.Error(t, err)
		assert.Nil(t, response)
		assert.Contains(t, err.Error(), "timeout must be between")
		assert.Nil(t, mockClient.CapturedInput)
	})
}
