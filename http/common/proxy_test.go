package common

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http/validate"

	httpactions "github.com/smartcontractkit/capabilities/http/protos"

	gateway "github.com/smartcontractkit/chainlink-common/pkg/types/gateway"

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

func TestNewDirect(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		cfg := HTTPClientConfig{}
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
		require.NoError(t, err)
		require.NotNil(t, proxy)
		require.NotNil(t, proxy.client)
	})

	t.Run("with custom config", func(t *testing.T) {
		cfg := HTTPClientConfig{
			AllowedPorts:   []int{8080, 9090},
			AllowedSchemes: []string{"https"},
			BlockedIPs:     []string{"192.168.1.1"},
			AllowedIPs:     []string{"10.0.0.1"},
		}
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
		require.NoError(t, err)
		require.NotNil(t, proxy)
		require.NotNil(t, proxy.client)
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
		cfg := validClientCfg(t, server.URL)
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
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

		response, err := proxy.SendRequest(t.Context(), outbound(input))

		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
		require.Equal(t, "test-value", response.Headers["X-Test-Header"])
		require.Equal(t, "text/plain", response.Headers["Content-Type"])
		require.Equal(t, "success", string(response.Body))
	})

	t.Run("echo request", func(t *testing.T) {
		cfg := validClientCfg(t, server.URL)
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
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

		response, err := proxy.SendRequest(t.Context(), outbound(input))

		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)
		require.Equal(t, "echo", string(response.Body))
	})

	t.Run("timeout", func(t *testing.T) {
		cfg := validClientCfg(t, server.URL)
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
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

		_, err = proxy.SendRequest(t.Context(), outbound(input))

		// We should get a timeout error
		require.Error(t, err)
		require.Contains(t, err.Error(), "deadline exceeded")
	})

	t.Run("invalid url", func(t *testing.T) {
		cfg := validClientCfg(t, server.URL)
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     "http://invalid-url-that-does-not-exist.example",
			Timeout: durationpb.New(1000 * time.Millisecond),
			Body:    []byte{},
		}

		_, err = proxy.SendRequest(t.Context(), outbound(input))

		require.Error(t, err)
	})

}

func TestSendRequest_MultiHeaders(t *testing.T) {

	// verifyBackwardCompatibility checks that all keys in MultiHeaders are also present in Headers
	// with non-empty values, ensuring backward compatibility with the deprecated Headers field.
	verifyBackwardCompatibility := func(t *testing.T, headers map[string]string, multiHeaders map[string][]string) {
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

		cfg := validClientCfg(t, server.URL)
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     server.URL,
			Timeout: durationpb.New(1000 * time.Millisecond),
			Body:    []byte{},
		}

		response, err := proxy.SendRequest(t.Context(), outbound(input))
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)

		// Verify MultiHeaders contains all Set-Cookie values
		require.NotNil(t, response.MultiHeaders, "MultiHeaders should not be nil")
		setCookieHeader, ok := response.MultiHeaders["Set-Cookie"]
		require.True(t, ok, "Set-Cookie header should be in MultiHeaders")
		require.NotNil(t, setCookieHeader)
		require.Len(t, setCookieHeader, 3, "Should have 3 Set-Cookie headers")
		require.Contains(t, setCookieHeader, "sessionid=abc123; Path=/; HttpOnly")
		require.Contains(t, setCookieHeader, "csrf_token=xyz789; Path=/; Secure")
		require.Contains(t, setCookieHeader, "pref=dark; Path=/")

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

		cfg := validClientCfg(t, server.URL)
		lggr := logger.Test(t)
		proxy, err := NewDirect(cfg, lggr)
		require.NoError(t, err)

		input := &httpactions.Request{
			Method:  http.MethodGet,
			Url:     server.URL,
			Timeout: durationpb.New(1000 * time.Millisecond),
			Body:    []byte{},
		}

		response, err := proxy.SendRequest(t.Context(), outbound(input))
		require.NoError(t, err)
		require.Equal(t, 200, response.StatusCode)

		// Verify MultiHeaders contains single value
		require.NotNil(t, response.MultiHeaders)
		contentTypeHeader, ok := response.MultiHeaders["Content-Type"]
		require.True(t, ok, "Content-Type header should be in MultiHeaders")
		require.NotNil(t, contentTypeHeader)
		require.Len(t, contentTypeHeader, 1, "Should have 1 Content-Type header")
		require.Equal(t, "application/json", contentTypeHeader[0])

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
		// Said out loud, because the default is https and these servers are not: a
		// direct request reaches the internet with nothing in front of it, so what it
		// may reach is never inferred.
		AllowedSchemes: []string{"http"},
	}
}

// ResponseOf is what an Outbound answers with; these cover the headers in it,
// which is where the awkwardness lives - a header may repeat, and may hold bytes
// that are not text.
func TestResponseOf(t *testing.T) {
	headersOf := func(h http.Header) (map[string][]string, map[string]string) {
		response := ResponseOf(&http.Response{StatusCode: http.StatusOK, Header: h}, nil, 0)
		return response.MultiHeaders, response.Headers
	}

	t.Run("empty header", func(t *testing.T) {
		h := make(http.Header)
		multi, single := headersOf(h)
		require.Empty(t, multi)
		require.Empty(t, single)
	})

	t.Run("single value per key", func(t *testing.T) {
		h := http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"abc-123"},
		}
		multi, single := headersOf(h)
		require.Len(t, multi, 2)
		require.Len(t, single, 2)
		require.Equal(t, []string{"application/json"}, multi["Content-Type"])
		require.Equal(t, []string{"abc-123"}, multi["X-Request-Id"])
		require.Equal(t, "application/json", single["Content-Type"])
		require.Equal(t, "abc-123", single["X-Request-Id"])
	})

	t.Run("multiple values per key", func(t *testing.T) {
		h := http.Header{
			"Set-Cookie": []string{"a=1", "b=2", "c=3"},
			"Accept":     []string{"application/json"},
		}
		multi, single := headersOf(h)
		require.Len(t, multi, 2)
		require.Len(t, single, 2)
		require.Equal(t, []string{"a=1", "b=2", "c=3"}, multi["Set-Cookie"])
		require.Equal(t, []string{"application/json"}, multi["Accept"])
		require.Equal(t, "a=1,b=2,c=3", single["Set-Cookie"])
		require.Equal(t, "application/json", single["Accept"])
	})

	t.Run("skips empty value slices", func(t *testing.T) {
		h := http.Header{
			"X-Good": []string{"value"},
			"X-Bad":  []string{},
		}
		multi, single := headersOf(h)
		require.Len(t, multi, 1)
		require.Len(t, single, 1)
		require.Contains(t, multi, "X-Good")
		require.NotContains(t, multi, "X-Bad")
		require.Equal(t, "value", single["X-Good"])
	})

	t.Run("preserves valid UTF-8 untouched", func(t *testing.T) {
		h := http.Header{
			"Content-Type": []string{"application/json"},
			"X-Multi":      []string{"héllo", "wörld", "日本語"},
		}
		multi, single := headersOf(h)
		require.Equal(t, []string{"application/json"}, multi["Content-Type"])
		require.Equal(t, []string{"héllo", "wörld", "日本語"}, multi["X-Multi"])
		require.Equal(t, "héllo,wörld,日本語", single["X-Multi"])
	})

	t.Run("sanitizes invalid UTF-8 and stays marshalable", func(t *testing.T) {
		invalidVal := "prefix" + string([]byte{0xff, 0xfe}) + "suffix"
		invalidKey := "X-Bad" + string([]byte{0xff})
		h := http.Header{
			"X-Good":   []string{"clean"},
			invalidKey: []string{invalidVal, "also-clean"},
		}
		multi, single := headersOf(h)

		require.Equal(t, []string{"clean"}, multi["X-Good"])

		sanitizedKey := SanitizeUTF8(invalidKey)
		require.True(t, utf8.ValidString(sanitizedKey))
		require.Contains(t, multi, sanitizedKey)
		require.Len(t, multi[sanitizedKey], 2)
		require.True(t, utf8.ValidString(multi[sanitizedKey][0]))
		require.Equal(t, "also-clean", multi[sanitizedKey][1])
		require.True(t, utf8.ValidString(single[sanitizedKey]))

		// Which is what a workflow's response is built from, and what has to be valid
		// UTF-8 for that to cross gRPC. See action.responseHeaders.
		for _, values := range multi {
			for _, value := range values {
				require.True(t, utf8.ValidString(value))
			}
		}
	})
}

func TestSanitizeUTF8(t *testing.T) {
	t.Run("returns valid strings unchanged", func(t *testing.T) {
		for _, s := range []string{"", "ascii", "héllo wörld", "日本語", "emoji 🚀"} {
			require.Equal(t, s, SanitizeUTF8(s))
		}
	})

	t.Run("replaces invalid bytes with U+FFFD", func(t *testing.T) {
		got := SanitizeUTF8("a" + string([]byte{0xff}) + "b")
		require.True(t, utf8.ValidString(got))
		require.Equal(t, "a�b", got)
	})
}

// outbound is the capability's request as an Outbound is given it. The capability
// does this conversion for real (see action.outboundRequest); here it keeps these
// tests written in the shape they were, which is the shape a reader knows.
func outbound(input *httpactions.Request) gateway.OutboundHTTPRequest {
	request := gateway.OutboundHTTPRequest{
		URL:       input.Url,
		Method:    input.Method,
		Headers:   input.Headers, //nolint:staticcheck // Headers is deprecated but is what these tests set
		Body:      input.Body,
		TimeoutMs: uint32(input.Timeout.AsDuration().Milliseconds()), //nolint:gosec // G115 - a test's timeout
	}
	if len(input.MultiHeaders) > 0 {
		request.Headers = nil
		request.MultiHeaders = make(map[string][]string, len(input.MultiHeaders))
		for name, values := range input.MultiHeaders {
			request.MultiHeaders[name] = values.GetValues()
		}
	}
	return request
}
