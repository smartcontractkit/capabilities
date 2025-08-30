package common

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/http_action/validate"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	httpactions "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

func newTestValidator(t *testing.T) ResponseValidator {
	lggr := logger.Test(t)
	limitsFactory := limits.Factory{
		Logger: lggr,
	}

	validator, err := validate.NewValidator(lggr, limitsFactory)
	require.NoError(t, err)
	return validator
}

func TestNewHTTPClientProxy(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		cfg := ServiceConfig{}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator)
		require.NoError(t, err)
		require.NotNil(t, proxy)
		require.NotNil(t, proxy.client)
	})

	t.Run("with custom config", func(t *testing.T) {
		cfg := ServiceConfig{
			HTTPClientConfig: HTTPClientConfig{
				AllowedPorts:   []int{8080, 9090},
				AllowedSchemes: []string{"https"},
				BlockedIPs:     []string{"192.168.1.1"},
				AllowedIPs:     []string{"10.0.0.1"},
			},
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator)
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
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}
	ctx := contexts.WithCRE(t.Context(), contexts.CRE{Owner: metadata.WorkflowOwner, Workflow: metadata.WorkflowID})

	t.Run("successful request", func(t *testing.T) {
		cfg := ServiceConfig{
			HTTPClientConfig: validClientCfg(t, server.URL),
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator)
		require.NoError(t, err)

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
			HTTPClientConfig: validClientCfg(t, server.URL),
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator)
		require.NoError(t, err)

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
			HTTPClientConfig: validClientCfg(t, server.URL),
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator)
		require.NoError(t, err)

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
			HTTPClientConfig: validClientCfg(t, server.URL),
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator)
		require.NoError(t, err)

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
			// Write a response larger than our limit (over 1MB to trigger the validator)
			_, _ = w.Write(bytes.Repeat([]byte("a"), 2*1024*1024))
		}))
		defer largeServer.Close()

		cfg := ServiceConfig{
			HTTPClientConfig: validClientCfg(t, largeServer.URL),
		}
		lggr := logger.Test(t)

		// Use the validator that rejects responses > 1MB
		validator := newTestValidator(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:    http.MethodGet,
			Url:       largeServer.URL,
			TimeoutMs: 1000,
			Body:      []byte{},
		}

		_, err = proxy.SendRequest(ctx, metadata, input)

		// Should get an error because response is too large
		require.Error(t, err)
		require.Contains(t, err.Error(), "ResponseSizeLimit limited")
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
