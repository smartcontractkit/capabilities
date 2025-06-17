package action

import (
	"math"
	"testing"

	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
)

func customConfig() common.ServiceConfig {
	return common.ServiceConfig{
		LimitsConfig: common.LimitsConfig{
			MaxTimeoutMs:         1_000_000_000,    // 10 seconds
			MaxResponseBytes:     5 * 1024 * 1024,  // 5 MB
			MaxHeaderCount:       50,               // 50 headers
			MaxHeaderKeyLength:   256,              // 256 bytes
			MaxHeaderValueLength: 1024,             // 1024 bytes
			MaxBodyLength:        10 * 1024 * 1024, // 20 MB
		},
	}
}

func TestValidateAndApplyDefaults(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		input := &http.Request{
			Url:       "https://example.com",
			Method:    "POST",
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      []byte(`{"foo":"bar"}`),
			TimeoutMs: 1000,
		}
		out, err := ValidateAndApplyDefaults(input, customConfig())
		require.NoError(t, err)
		require.Equal(t, "https://example.com", out.Url)
		require.Equal(t, "POST", out.Method)
		require.Equal(t, input.Headers, out.Headers)
		require.Equal(t, input.Body, out.Body)
		require.Equal(t, int32(1000), out.TimeoutMs)
	})

	t.Run("empty URL", func(t *testing.T) {
		input := &http.Request{Url: ""}
		_, err := ValidateAndApplyDefaults(input, customConfig())
		require.ErrorContains(t, err, "URL must not be empty")
	})

	t.Run("timeout config", func(t *testing.T) {
		input := &http.Request{Url: "https://foo", Method: "GET", TimeoutMs: 0}
		out, err := ValidateAndApplyDefaults(input, customConfig())
		require.NoError(t, err)
		require.Equal(t, int32(customConfig().LimitsConfig.MaxTimeoutMs), out.TimeoutMs)
	})

	t.Run("header count limits", func(t *testing.T) {
		input := &http.Request{Url: "https://foo", Method: "GET", Headers: map[string]string{}}
		for i := 0; i < 51; i++ {
			input.Headers[string(rune('A'+i))] = "v"
		}
		_, err := ValidateAndApplyDefaults(input, customConfig())
		require.ErrorContains(t, err, "too many headers")
	})

	t.Run("header key/value length limits", func(t *testing.T) {
		longKey := make([]byte, 257)
		for i := range longKey {
			longKey[i] = 'a'
		}
		input := &http.Request{Url: "https://foo", Method: "GET", Headers: map[string]string{string(longKey): "v"}}
		_, err := ValidateAndApplyDefaults(input, customConfig())
		require.ErrorContains(t, err, "header key too long")

		longVal := make([]byte, 1025)
		for i := range longVal {
			longVal[i] = 'b'
		}
		input2 := &http.Request{Url: "https://foo", Method: "GET", Headers: map[string]string{"k": string(longVal)}}
		_, err = ValidateAndApplyDefaults(input2, customConfig())
		require.ErrorContains(t, err, "header value for \"k\" too long")
	})

	t.Run("body size limit", func(t *testing.T) {
		bigBody := make([]byte, 10*1024*1024+1)
		input := &http.Request{Url: "https://foo", Method: "GET", Body: bigBody}
		_, err := ValidateAndApplyDefaults(input, customConfig())
		require.ErrorContains(t, err, "body too large")
	})

	t.Run("timeout limit", func(t *testing.T) {
		input := &http.Request{Url: "https://foo", Method: "GET", TimeoutMs: 1_000_000_001}
		_, err := ValidateAndApplyDefaults(input, customConfig())
		require.ErrorContains(t, err, "timeout must be between 0 and")
	})
}
func TestValidatedServiceConfig(t *testing.T) {
	t.Run("returns defaults when config is zero", func(t *testing.T) {
		cfg := &common.ServiceConfig{}
		out, err := ValidatedServiceConfig(cfg)
		require.NoError(t, err)
		require.Equal(t, uint32(defaultMaxTimeoutMs), out.LimitsConfig.MaxTimeoutMs)
		require.Equal(t, uint32(defaultMaxHeaderCount), out.LimitsConfig.MaxHeaderCount)
		require.Equal(t, uint32(defaultMaxHeaderKeyLength), out.LimitsConfig.MaxHeaderKeyLength)
		require.Equal(t, uint32(defaultMaxHeaderValueLength), out.LimitsConfig.MaxHeaderValueLength)
		require.Equal(t, uint32(defaultMaxBodyLength), out.LimitsConfig.MaxBodyLength)
	})

	t.Run("returns custom values when set", func(t *testing.T) {
		cfg := &common.ServiceConfig{
			LimitsConfig: common.LimitsConfig{
				MaxTimeoutMs:         1234,
				MaxHeaderCount:       12,
				MaxHeaderKeyLength:   42,
				MaxHeaderValueLength: 99,
				MaxBodyLength:        2048,
			},
		}
		out, err := ValidatedServiceConfig(cfg)
		require.NoError(t, err)
		require.Equal(t, uint32(1234), out.LimitsConfig.MaxTimeoutMs)
		require.Equal(t, uint32(12), out.LimitsConfig.MaxHeaderCount)
		require.Equal(t, uint32(42), out.LimitsConfig.MaxHeaderKeyLength)
		require.Equal(t, uint32(99), out.LimitsConfig.MaxHeaderValueLength)
		require.Equal(t, uint32(2048), out.LimitsConfig.MaxBodyLength)
	})

	t.Run("returns error if MaxTimeoutMs exceeds int32", func(t *testing.T) {
		cfg := &common.ServiceConfig{
			LimitsConfig: common.LimitsConfig{
				MaxTimeoutMs: math.MaxInt32 + 1,
			},
		}
		_, err := ValidatedServiceConfig(cfg)
		require.ErrorContains(t, err, "MaxTimeoutMs exceeds int32 maximum")
	})
}
