package action

import (
	"testing"

	"github.com/smartcontractkit/capabilities/http/common"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
)

func defaultConfig() common.ServiceConfig {
	return common.ServiceConfig{}
}

func customConfig() common.ServiceConfig {
	return common.ServiceConfig{
		LimitsConfig: common.LimitsConfig{
			MaxTimeoutMs:         1_000_000_000,    // 10 seconds
			MaxResponseBytes:     5 * 1024 * 1024,  // 5 MB
			MaxHeaderCount:       100,              // 100 headers
			MaxHeaderKeyLength:   512,              // 512 bytes
			MaxHeaderValueLength: 2048,             // 2048 bytes
			MaxBodyLength:        20 * 1024 * 1024, // 20 MB
		},
	}
}

func TestValidateAndApplyDefaults(t *testing.T) {
	t.Run("valid input", func(t *testing.T) {
		input := &http.Inputs{
			Url:       "https://example.com",
			Method:    "POST",
			Headers:   map[string]string{"Content-Type": "application/json"},
			Body:      []byte(`{"foo":"bar"}`),
			TimeoutMs: 1000,
		}
		out, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.NoError(t, err)
		require.Equal(t, "https://example.com", out.Url)
		require.Equal(t, "POST", out.Method)
		require.Equal(t, input.Headers, out.Headers)
		require.Equal(t, input.Body, out.Body)
		require.Equal(t, int32(1000), out.TimeoutMs)
	})

	t.Run("empty URL", func(t *testing.T) {
		input := &http.Inputs{Url: ""}
		_, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.ErrorContains(t, err, "URL must not be empty")
	})

	t.Run("unsupported method", func(t *testing.T) {
		input := &http.Inputs{Url: "https://foo", Method: "FOOBAR"}
		_, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.ErrorContains(t, err, "unsupported HTTP method")
	})

	t.Run("default method", func(t *testing.T) {
		input := &http.Inputs{Url: "https://foo", Method: ""}
		out, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.NoError(t, err)
		require.Equal(t, "GET", out.Method)
	})

	t.Run("timeout default and custom", func(t *testing.T) {
		input := &http.Inputs{Url: "https://foo", Method: "GET", TimeoutMs: 0}
		out, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.NoError(t, err)
		require.Equal(t, int32(defaultMaxTimeoutMs), out.TimeoutMs)
		out, err = ValidateAndApplyDefaults(input, customConfig())
		require.NoError(t, err)
		require.Equal(t, int32(customConfig().LimitsConfig.MaxTimeoutMs), out.TimeoutMs)
	})

	t.Run("header count limits", func(t *testing.T) {
		input := &http.Inputs{Url: "https://foo", Method: "GET", Headers: map[string]string{}}
		for i := 0; i < 51; i++ {
			input.Headers[string(rune('A'+i))] = "v"
		}
		_, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.ErrorContains(t, err, "too many headers")
		_, err = ValidateAndApplyDefaults(input, customConfig())
		require.NoError(t, err)
	})

	t.Run("header key/value length limits", func(t *testing.T) {
		longKey := make([]byte, 257)
		for i := range longKey {
			longKey[i] = 'a'
		}
		input := &http.Inputs{Url: "https://foo", Method: "GET", Headers: map[string]string{string(longKey): "v"}}
		_, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.ErrorContains(t, err, "header key too long")

		longVal := make([]byte, 1025)
		for i := range longVal {
			longVal[i] = 'b'
		}
		input2 := &http.Inputs{Url: "https://foo", Method: "GET", Headers: map[string]string{"k": string(longVal)}}
		_, err = ValidateAndApplyDefaults(input2, defaultConfig())
		require.ErrorContains(t, err, "header value for \"k\" too long")

		cfg := customConfig()
		_, err = ValidateAndApplyDefaults(input, cfg)
		require.NoError(t, err)
		_, err = ValidateAndApplyDefaults(input2, cfg)
		require.NoError(t, err)
	})

	t.Run("body size limit", func(t *testing.T) {
		bigBody := make([]byte, 10*1024*1024+1)
		input := &http.Inputs{Url: "https://foo", Method: "GET", Body: bigBody}
		_, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.ErrorContains(t, err, "body too large")
		_, err = ValidateAndApplyDefaults(input, customConfig())
		require.NoError(t, err)
	})

	t.Run("timeout limit", func(t *testing.T) {
		input := &http.Inputs{Url: "https://foo", Method: "GET", TimeoutMs: 999999999}
		_, err := ValidateAndApplyDefaults(input, defaultConfig())
		require.ErrorContains(t, err, "timeout must be between 0 and")
		_, err = ValidateAndApplyDefaults(input, customConfig())
		require.NoError(t, err)
	})
}
