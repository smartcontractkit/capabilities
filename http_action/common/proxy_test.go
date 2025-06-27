package common

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	httpactions "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
)

func TestNewHTTPClientProxy(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		cfg := ServiceConfig{
			OutgoingRateLimiter: rateLimiterConfig(),
		}
		lggr := logger.Test(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr)
		require.NoError(t, err)
		require.NotNil(t, proxy)
		require.NotNil(t, proxy.client)
		require.Equal(t, []int{80, 443}, proxy.cfg.HTTPClientConfig.AllowedPorts)
		require.Equal(t, []string{"http", "https"}, proxy.cfg.HTTPClientConfig.AllowedSchemes)
	})

	t.Run("with custom config", func(t *testing.T) {
		cfg := ServiceConfig{
			HTTPClientConfig: HTTPClientConfig{
				AllowedPorts:   []int{8080, 9090},
				AllowedSchemes: []string{"https"},
				BlockedIPs:     []string{"192.168.1.1"},
				AllowedIPs:     []string{"10.0.0.1"},
			},
			LimitsConfig: LimitsConfig{
				MaxResponseBytes: 1024,
			},
			OutgoingRateLimiter: rateLimiterConfig(),
		}
		lggr := logger.Test(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr)
		require.NoError(t, err)
		require.NotNil(t, proxy)
		require.NotNil(t, proxy.client)
		require.Equal(t, cfg, proxy.cfg)
	})
}

func TestSendRequest(t *testing.T) {
	// Setup a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check headers from request
		userAgent := r.Header.Get("User-Agent")
		contentType := r.Header.Get("Content-Type")

		// Read request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusInternalServerError)
			return
		}

		// Set response headers
		w.Header().Set("X-Test-Header", "test-value")
		w.Header().Set("Content-Type", contentType)

		// Write response
		if string(body) == "echo" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(body)
		} else if userAgent == "timeout-client" {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("timeout test"))
		} else {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("success"))
		}
	}))
	defer server.Close()

	t.Run("successful request", func(t *testing.T) {
		cfg := ServiceConfig{
			LimitsConfig: LimitsConfig{
				MaxResponseBytes: 1024,
			},
			HTTPClientConfig:    validClientCfg(t, server.URL),
			OutgoingRateLimiter: rateLimiterConfig(),
		}
		lggr := logger.Test(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr)
		require.NoError(t, err)

		ctx := context.Background()
		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}

		input := &httpactions.Request{
			Method:    http.MethodGet,
			Url:       server.URL,
			TimeoutMs: 1000,
			Headers: map[string]string{
				"Content-Type": "text/plain",
				"User-Agent":   "test-client",
			},
			Body: []byte("success"),
		}

		response, err := proxy.SendRequest(ctx, metadata, input)

		require.NoError(t, err)
		require.Equal(t, uint32(200), response.StatusCode)
		require.Equal(t, "test-value", response.Headers["X-Test-Header"])
		require.Equal(t, "text/plain", response.Headers["Content-Type"])
		require.Equal(t, "success", string(response.Body))
	})

	t.Run("echo request", func(t *testing.T) {
		cfg := ServiceConfig{
			LimitsConfig: LimitsConfig{
				MaxResponseBytes: 1024,
			},
			HTTPClientConfig:    validClientCfg(t, server.URL),
			OutgoingRateLimiter: rateLimiterConfig(),
		}
		lggr := logger.Test(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr)
		require.NoError(t, err)

		ctx := context.Background()
		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}

		input := &httpactions.Request{
			Method:    http.MethodPost,
			Url:       server.URL,
			TimeoutMs: 1000,
			Headers: map[string]string{
				"Content-Type": "text/plain",
			},
			Body: []byte("echo"),
		}

		response, err := proxy.SendRequest(ctx, metadata, input)

		require.NoError(t, err)
		require.Equal(t, uint32(200), response.StatusCode)
		require.Equal(t, "echo", string(response.Body))
	})

	t.Run("timeout", func(t *testing.T) {
		cfg := ServiceConfig{
			LimitsConfig: LimitsConfig{
				MaxResponseBytes: 1024,
			},
			HTTPClientConfig:    validClientCfg(t, server.URL),
			OutgoingRateLimiter: rateLimiterConfig(),
		}
		lggr := logger.Test(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr)
		require.NoError(t, err)

		ctx := context.Background()
		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}

		input := &httpactions.Request{
			Method:    http.MethodGet,
			Url:       server.URL,
			TimeoutMs: 100, // Set timeout to 100ms, which should be less than the server delay
			Headers: map[string]string{
				"User-Agent": "timeout-client",
			},
			Body: []byte{},
		}

		_, err = proxy.SendRequest(ctx, metadata, input)

		// We should get a timeout error
		require.Error(t, err)
		require.Contains(t, err.Error(), "deadline exceeded")
	})

	t.Run("invalid url", func(t *testing.T) {
		cfg := ServiceConfig{
			LimitsConfig: LimitsConfig{
				MaxResponseBytes: 1024,
			},
			HTTPClientConfig:    validClientCfg(t, server.URL),
			OutgoingRateLimiter: rateLimiterConfig(),
		}
		lggr := logger.Test(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr)
		require.NoError(t, err)

		ctx := context.Background()
		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}

		input := &httpactions.Request{
			Method:    http.MethodGet,
			Url:       "http://invalid-url-that-does-not-exist.example",
			TimeoutMs: 1000,
			Body:      []byte{},
		}

		_, err = proxy.SendRequest(ctx, metadata, input)

		require.Error(t, err)
	})

	t.Run("max response bytes limit", func(t *testing.T) {
		// Create a local test server that returns a large response
		largeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			// Write a response larger than our limit
			_, _ = w.Write(bytes.Repeat([]byte("a"), 100))
		}))
		defer largeServer.Close()

		cfg := ServiceConfig{
			LimitsConfig: LimitsConfig{
				MaxResponseBytes: 50, // Limit to 50 bytes
			},
			HTTPClientConfig:    validClientCfg(t, largeServer.URL),
			OutgoingRateLimiter: rateLimiterConfig(),
		}
		lggr := logger.Test(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr)
		require.NoError(t, err)

		ctx := context.Background()
		metadata := capabilities.RequestMetadata{
			WorkflowID:          "wf1",
			WorkflowExecutionID: "exec1",
			WorkflowOwner:       "owner1",
		}

		input := &httpactions.Request{
			Method:    http.MethodGet,
			Url:       largeServer.URL,
			TimeoutMs: 1000,
			Body:      []byte{},
		}

		response, err := proxy.SendRequest(ctx, metadata, input)

		require.NoError(t, err)
		// Only the first MaxResponseBytes should be read
		require.Equal(t, 50, len(response.Body))
		require.Equal(t, string(bytes.Repeat([]byte("a"), 50)), string(response.Body))
	})
}

func validClientCfg(t *testing.T, urlStr string) HTTPClientConfig {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		panic(err)
	}
	host := parsedURL.Host
	_, portStr, err := net.SplitHostPort(host)
	require.NoError(t, err)
	port, err := strconv.Atoi(portStr)
	require.NoError(t, err)
	return HTTPClientConfig{
		AllowedPorts: []int{port},
		AllowedIPs:   []string{"127.0.0.1"},
	}
}

func rateLimiterConfig() ratelimit.RateLimiterConfig {
	return ratelimit.RateLimiterConfig{
		GlobalRPS:      1000,
		GlobalBurst:    1000,
		PerSenderRPS:   1000,
		PerSenderBurst: 1000,
	}
}
