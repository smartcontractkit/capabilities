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
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http_action/validate"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	httpactions "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

func newTestValidator(t *testing.T) RequestValidator {
	lggr := logger.Test(t)
	limitsFactory := limits.Factory{
		Logger: lggr,
	}

	validator, err := validate.NewValidator(lggr, limitsFactory)
	require.NoError(t, err)
	return validator
}

func newTestMetrics(t *testing.T) *Metrics {
	m, err := NewMetrics()
	require.NoError(t, err)
	return m
}

func TestNewHTTPClientProxy(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		cfg := ServiceConfig{}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
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
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
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

	t.Run("successful request", func(t *testing.T) {
		cfg := ServiceConfig{
			HTTPClientConfig: validClientCfg(t, server.URL),
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     server.URL,
			Timeout: durationpb.New(1000 * time.Millisecond),
			Headers: map[string]string{
				"Content-Type": "text/plain",
				"User-Agent":   "test-client",
			},
			Body: []byte("success"),
		}

		response, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())

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
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodPost,
			Url:     server.URL,
			Timeout: durationpb.New(1000 * time.Millisecond),
			Headers: map[string]string{
				"Content-Type": "text/plain",
			},
			Body: []byte("echo"),
		}

		response, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())

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
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     server.URL,
			Timeout: durationpb.New(100 * time.Millisecond), // Set timeout to 100ms, which should be less than the server delay
			Headers: map[string]string{
				"User-Agent": "timeout-client",
			},
			Body: []byte{},
		}

		_, _, err = proxy.SendRequest(t.Context(), metadata, input, time.Now())

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
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     "http://invalid-url-that-does-not-exist.example",
			Timeout: durationpb.New(1000 * time.Millisecond),
			Body:    []byte{},
		}

		_, _, err = proxy.SendRequest(t.Context(), metadata, input, time.Now())

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
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     largeServer.URL,
			Timeout: durationpb.New(1000 * time.Millisecond),
			Body:    []byte{},
		}

		_, _, err = proxy.SendRequest(t.Context(), metadata, input, time.Now())

		// Should get an error because response is too large
		require.Error(t, err)
		require.Contains(t, err.Error(), "ResponseSizeLimit limited")
	})
}

func TestSendRequest_MultiHeaders(t *testing.T) {
	metadata := capabilities.RequestMetadata{
		WorkflowID:          "wf1",
		WorkflowExecutionID: "exec1",
		WorkflowOwner:       "owner1",
	}

	// verifyBackwardCompatibility checks that all keys in MultiHeaders are also present in Headers
	// with non-empty values, ensuring backward compatibility with the deprecated Headers field.
	verifyBackwardCompatibility := func(t *testing.T, headers map[string]string, multiHeaders map[string]*httpactions.HeaderValues) {
		for key := range multiHeaders {
			require.NotEmpty(t, headers[key], "Headers should contain %s for backward compatibility", key)
		}
	}

	t.Run("response with multiple Set-Cookie headers", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Set multiple Set-Cookie headers (cannot be comma-separated per RFC 6265)
			w.Header().Add("Set-Cookie", "sessionid=abc123; Path=/; HttpOnly")
			w.Header().Add("Set-Cookie", "csrf_token=xyz789; Path=/; Secure")
			w.Header().Add("Set-Cookie", "pref=dark; Path=/")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("success"))
		}))
		defer server.Close()

		cfg := ServiceConfig{
			HTTPClientConfig: validClientCfg(t, server.URL),
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     server.URL,
			Timeout: durationpb.New(1000 * time.Millisecond),
			Body:    []byte{},
		}

		response, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.Equal(t, uint32(200), response.StatusCode)

		// Verify MultiHeaders contains all Set-Cookie values
		require.NotNil(t, response.MultiHeaders, "MultiHeaders should not be nil")
		setCookieHeader, ok := response.MultiHeaders["Set-Cookie"]
		require.True(t, ok, "Set-Cookie header should be in MultiHeaders")
		require.NotNil(t, setCookieHeader)
		require.Len(t, setCookieHeader.Values, 3, "Should have 3 Set-Cookie headers")
		require.Contains(t, setCookieHeader.Values, "sessionid=abc123; Path=/; HttpOnly")
		require.Contains(t, setCookieHeader.Values, "csrf_token=xyz789; Path=/; Secure")
		require.Contains(t, setCookieHeader.Values, "pref=dark; Path=/")

		// Verify Headers field has comma-joined values (backward compatibility)
		require.Equal(t, "sessionid=abc123; Path=/; HttpOnly,csrf_token=xyz789; Path=/; Secure,pref=dark; Path=/", response.Headers["Set-Cookie"]) //nolint:staticcheck

		// Verify backward compatibility: all keys in MultiHeaders should be in Headers
		verifyBackwardCompatibility(t, response.Headers, response.MultiHeaders) //nolint:staticcheck
	})

	t.Run("response with single header value", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("success"))
		}))
		defer server.Close()

		cfg := ServiceConfig{
			HTTPClientConfig: validClientCfg(t, server.URL),
		}
		lggr := logger.Test(t)
		validator := newTestValidator(t)
		metrics := newTestMetrics(t)
		proxy, err := NewHTTPClientProxy(cfg, lggr, validator, metrics)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     server.URL,
			Timeout: durationpb.New(1000 * time.Millisecond),
			Body:    []byte{},
		}

		response, _, err := proxy.SendRequest(t.Context(), metadata, input, time.Now())
		require.NoError(t, err)
		require.Equal(t, uint32(200), response.StatusCode)

		// Verify MultiHeaders contains single value
		require.NotNil(t, response.MultiHeaders)
		contentTypeHeader, ok := response.MultiHeaders["Content-Type"]
		require.True(t, ok, "Content-Type header should be in MultiHeaders")
		require.NotNil(t, contentTypeHeader)
		require.Len(t, contentTypeHeader.Values, 1, "Should have 1 Content-Type header")
		require.Equal(t, "application/json", contentTypeHeader.Values[0])

		// Verify Headers field matches (backward compatibility)
		require.Equal(t, "application/json", response.Headers["Content-Type"]) //nolint:staticcheck // SA1019 testing deprecated Headers field

		// Verify backward compatibility: all keys in MultiHeaders should be in Headers
		verifyBackwardCompatibility(t, response.Headers, response.MultiHeaders) //nolint:staticcheck
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

func TestToResponseHeaders(t *testing.T) {
	t.Run("empty header", func(t *testing.T) {
		h := make(http.Header)
		multi, single := toResponseHeaders(h)
		require.Empty(t, multi)
		require.Empty(t, single)
	})

	t.Run("single value per key", func(t *testing.T) {
		h := http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"abc-123"},
		}
		multi, single := toResponseHeaders(h)
		require.Len(t, multi, 2)
		require.Len(t, single, 2)
		require.Equal(t, []string{"application/json"}, multi["Content-Type"].Values)
		require.Equal(t, []string{"abc-123"}, multi["X-Request-Id"].Values)
		require.Equal(t, "application/json", single["Content-Type"])
		require.Equal(t, "abc-123", single["X-Request-Id"])
	})

	t.Run("multiple values per key", func(t *testing.T) {
		h := http.Header{
			"Set-Cookie": []string{"a=1", "b=2", "c=3"},
			"Accept":     []string{"application/json"},
		}
		multi, single := toResponseHeaders(h)
		require.Len(t, multi, 2)
		require.Len(t, single, 2)
		require.Equal(t, []string{"a=1", "b=2", "c=3"}, multi["Set-Cookie"].Values)
		require.Equal(t, []string{"application/json"}, multi["Accept"].Values)
		require.Equal(t, "a=1,b=2,c=3", single["Set-Cookie"])
		require.Equal(t, "application/json", single["Accept"])
	})

	t.Run("skips empty value slices", func(t *testing.T) {
		h := http.Header{
			"X-Good": []string{"value"},
			"X-Bad":  []string{},
		}
		multi, single := toResponseHeaders(h)
		require.Len(t, multi, 1)
		require.Len(t, single, 1)
		require.Contains(t, multi, "X-Good")
		require.NotContains(t, multi, "X-Bad")
		require.Equal(t, "value", single["X-Good"])
	})
}
