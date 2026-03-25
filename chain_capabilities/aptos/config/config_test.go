package config

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalJSON(t *testing.T) {
	input := `{"chainId":"4","network":"aptos","creForwarderAddress":"0x26c93635e9af3ce8ba977ba6c3e4bc84b1cbfbeffe850a603ef0a7251aecbd55","deltaStage":1000000000,"observationPollerWorkersCount":17,"observationPollPeriod":2000000000,"chainHeightPollPeriod":3000000000,"unknownRequestsTTL":4000000000}`

	var cfg Config
	require.NoError(t, json.Unmarshal([]byte(input), &cfg))

	assert.Equal(t, "4", cfg.ChainID)
	assert.Equal(t, "aptos", cfg.Network)
	assert.EqualValues(t, 17, cfg.ObservationPollerWorkersCount)
	assert.Equal(t, time.Second, cfg.DeltaStage)
	assert.Equal(t, 2*time.Second, cfg.ObservationPollPeriod)
	assert.Equal(t, 3*time.Second, cfg.ChainHeightPollPeriod)
	assert.Equal(t, 4*time.Second, cfg.UnknownRequestsTTL)

	expectedAddr := [32]byte{
		0x26, 0xc9, 0x36, 0x35, 0xe9, 0xaf, 0x3c, 0xe8,
		0xba, 0x97, 0x7b, 0xa6, 0xc3, 0xe4, 0xbc, 0x84,
		0xb1, 0xcb, 0xfb, 0xef, 0xfe, 0x85, 0x0a, 0x60,
		0x3e, 0xf0, 0xa7, 0x25, 0x1a, 0xec, 0xbd, 0x55,
	}
	assert.Equal(t, expectedAddr, cfg.CREForwarderAddress)
}
