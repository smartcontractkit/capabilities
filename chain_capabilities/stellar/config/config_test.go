package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		input := `{
			"chainId":"stellar-testnet",
			"network":"stellar",
			"creForwarderAddress":"CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
			"deltaStage":1000000000,
			"observationPollerWorkersCount":17,
			"observationPollPeriod":2000000000,
			"unknownRequestsTTL":4000000000,
			"isLocal":true
		}`

		var cfg Config
		require.NoError(t, json.Unmarshal([]byte(input), &cfg))

		assert.Equal(t, "stellar-testnet", cfg.ChainID)
		assert.Equal(t, "stellar", cfg.Network)
		assert.Equal(t, "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC", cfg.CREForwarderAddress)
		assert.EqualValues(t, 17, cfg.ObservationPollerWorkersCount)
		assert.Equal(t, time.Second, cfg.DeltaStage)
		assert.Equal(t, 2*time.Second, cfg.ObservationPollPeriod)
		assert.Equal(t, 4*time.Second, cfg.UnknownRequestsTTL)
		assert.True(t, cfg.IsLocal)
	})

	t.Run("missing network", func(t *testing.T) {
		t.Parallel()
		input := `{"chainId":"stellar-testnet"}`

		var cfg Config
		err := json.Unmarshal([]byte(input), &cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "network is required")
	})

	t.Run("missing chainId", func(t *testing.T) {
		t.Parallel()
		input := `{"network":"stellar"}`

		var cfg Config
		err := json.Unmarshal([]byte(input), &cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "chainId is required")
	})

	t.Run("missing creForwarderAddress", func(t *testing.T) {
		t.Parallel()
		input := `{"network":"stellar","chainId":"stellar-testnet"}`

		var cfg Config
		err := json.Unmarshal([]byte(input), &cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "creForwarderAddress is required")
	})

	t.Run("invalid creForwarderAddress", func(t *testing.T) {
		t.Parallel()
		input := `{
			"network":"stellar",
			"chainId":"stellar-testnet",
			"creForwarderAddress":"GAAZI4TCR3TY5OJHCTJC2A4QSY6CJWJH5IAJTGKIN2ER7LBNVKOCCWN7"
		}`

		var cfg Config
		err := json.Unmarshal([]byte(input), &cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "creForwarderAddress")
		require.Contains(t, err.Error(), "invalid contract address")
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		var cfg Config
		err := json.Unmarshal([]byte("{"), &cfg)
		require.Error(t, err)
	})

	t.Run("optional forwarderLookbackLedgers", func(t *testing.T) {
		t.Parallel()
		input := `{
			"chainId":"stellar-testnet",
			"network":"stellar",
			"creForwarderAddress":"CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
			"forwarderLookbackLedgers":250
		}`
		var cfg Config
		require.NoError(t, json.Unmarshal([]byte(input), &cfg))
		assert.EqualValues(t, 250, cfg.ForwarderLookbackLedgers)
	})
}
