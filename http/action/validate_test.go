package action

import (
	"math"
	"testing"

	"github.com/smartcontractkit/capabilities/http/action/common"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
)

func customConfig() common.ServiceConfig {
	return common.ServiceConfig{
		LimitsConfig: common.LimitsConfig{
			MaxTimeoutMs:         1_000_000_000,    // 10 seconds
			MaxHeaderCount:       50,               // 50 headers
			MaxHeaderKeyLength:   256,              // 256 bytes
			MaxHeaderValueLength: 1024,             // 1024 bytes
			MaxRequestBytes:      10 * 1024 * 1024, // 20 MB
			MaxResponseBytes:     5 * 1024 * 1024,  // 5 MB
		},
	}
}

func TestApplyDefaultsAndValidate(t *testing.T) {
	t.Parallel()

	t.Run("applies defaults when zero values provided", func(t *testing.T) {
		cfg := &common.ServiceConfig{
			LimitsConfig: common.LimitsConfig{},
		}
		out, err := ApplyDefaultsAndValidate(cfg)
		require.NoError(t, err)
		require.Equal(t, uint32(defaultMaxTimeoutMs), out.LimitsConfig.MaxTimeoutMs)
		require.Equal(t, uint32(defaultMaxHeaderCount), out.LimitsConfig.MaxHeaderCount)
		require.Equal(t, uint32(defaultMaxHeaderKeyLength), out.LimitsConfig.MaxHeaderKeyLength)
		require.Equal(t, uint32(defaultMaxHeaderValueLength), out.LimitsConfig.MaxHeaderValueLength)
		require.Equal(t, uint32(defaultMaxBodyLength), out.LimitsConfig.MaxRequestBytes)
		require.Equal(t, uint32(defaultMaxBodyLength), out.LimitsConfig.MaxResponseBytes)
	})

	t.Run("keeps custom values", func(t *testing.T) {
		cfg := &common.ServiceConfig{
			LimitsConfig: common.LimitsConfig{
				MaxTimeoutMs:         1234,
				MaxHeaderCount:       12,
				MaxHeaderKeyLength:   34,
				MaxHeaderValueLength: 56,
				MaxRequestBytes:      78,
				MaxResponseBytes:     90,
			},
		}
		out, err := ApplyDefaultsAndValidate(cfg)
		require.NoError(t, err)
		require.Equal(t, uint32(1234), out.LimitsConfig.MaxTimeoutMs)
		require.Equal(t, uint32(12), out.LimitsConfig.MaxHeaderCount)
		require.Equal(t, uint32(34), out.LimitsConfig.MaxHeaderKeyLength)
		require.Equal(t, uint32(56), out.LimitsConfig.MaxHeaderValueLength)
		require.Equal(t, uint32(78), out.LimitsConfig.MaxRequestBytes)
		require.Equal(t, uint32(90), out.LimitsConfig.MaxResponseBytes)
	})

	t.Run("returns error if MaxTimeoutMs exceeds int32", func(t *testing.T) {
		cfg := &common.ServiceConfig{
			LimitsConfig: common.LimitsConfig{
				MaxTimeoutMs: math.MaxInt32 + 1,
			},
		}
		_, err := ApplyDefaultsAndValidate(cfg)
		require.ErrorContains(t, err, "MaxTimeoutMs exceeds int32 maximum")
	})
}

func TestValidatedRequest(t *testing.T) {
	t.Parallel()
	customConfig := customConfig()
	t.Run("valid input", func(t *testing.T) {
		t.Parallel()
		input := &http.Request{
			Url:       "https://example.com",
			Method:    "POST",
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      []byte(`{"foo":"bar"}`),
			TimeoutMs: 1000,
		}
		out, err := ValidatedRequest(input, customConfig)
		require.NoError(t, err)
		require.Equal(t, "https://example.com", out.Url)
		require.Equal(t, "POST", out.Method)
		require.Equal(t, input.Headers, out.Headers)
		require.Equal(t, input.Body, out.Body)
		require.Equal(t, int32(1000), out.TimeoutMs)
	})

	t.Run("empty URL", func(t *testing.T) {
		t.Parallel()
		input := &http.Request{Url: ""}
		_, err := ValidatedRequest(input, customConfig)
		require.ErrorContains(t, err, "URL must not be empty")
	})

	t.Run("timeout default and custom", func(t *testing.T) {
		t.Parallel()
		input := &http.Request{Url: "https://foo", Method: "GET", TimeoutMs: 0}
		out, err := ValidatedRequest(input, customConfig)
		require.NoError(t, err)
		require.Equal(t, int32(customConfig.LimitsConfig.MaxTimeoutMs), out.TimeoutMs)
	})

	t.Run("header count limits", func(t *testing.T) {
		t.Parallel()
		input := &http.Request{Url: "https://foo", Method: "GET", Headers: map[string]string{}}
		for i := 0; i < 51; i++ {
			input.Headers[string(rune('A'+i))] = "v"
		}
		_, err := ValidatedRequest(input, customConfig)
		require.ErrorContains(t, err, "too many headers")
	})

	t.Run("header key/value length limits", func(t *testing.T) {
		t.Parallel()
		longKey := make([]byte, 257)
		for i := range longKey {
			longKey[i] = 'a'
		}
		input := &http.Request{Url: "https://foo", Method: "GET", Headers: map[string]string{string(longKey): "v"}}
		_, err := ValidatedRequest(input, customConfig)
		require.ErrorContains(t, err, "header key too long")

		longVal := make([]byte, 1025)
		for i := range longVal {
			longVal[i] = 'b'
		}
		input2 := &http.Request{Url: "https://foo", Method: "GET", Headers: map[string]string{"k": string(longVal)}}
		_, err = ValidatedRequest(input2, customConfig)
		require.ErrorContains(t, err, "header value for \"k\" too long")
	})

	t.Run("body size limit", func(t *testing.T) {
		t.Parallel()
		bigBody := make([]byte, 10*1024*1024+1)
		input := &http.Request{Url: "https://foo", Method: "GET", Body: bigBody}
		_, err := ValidatedRequest(input, customConfig)
		require.ErrorContains(t, err, "body too large")
	})

	t.Run("timeout limit", func(t *testing.T) {
		t.Parallel()
		input := &http.Request{Url: "https://foo", Method: "GET", TimeoutMs: 1000000001}
		_, err := ValidatedRequest(input, customConfig)
		require.ErrorContains(t, err, "timeout must be between 0 and")
	})
}
