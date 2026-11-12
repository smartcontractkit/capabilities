package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTicker(t *testing.T) {
	for in, want := range map[string]Ticker{
		"BTC-USD":     {Base: "BTC", Quote: "USD"},
		"btc-usd":     {Base: "BTC", Quote: "USD"},
		"Eth/Usd":     {Base: "ETH", Quote: "USD"},
		"LINK_USDC":   {Base: "LINK", Quote: "USDC"},
		"  SOL-EUR  ": {Base: "SOL", Quote: "EUR"},
		"1INCH-USD":   {Base: "1INCH", Quote: "USD"},
	} {
		t.Run(in, func(t *testing.T) {
			got, err := ParseTicker(in)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}

	for _, in := range []string{
		"",
		"BTC",
		"BTCUSD",
		"BTC-",
		"-USD",
		"BTC-USD-EUR",
		"BTC USD",
		"BTC-USD?x=1",
		"../BTC-USD",
		"BTC-BTC",
		"ABCDEFGHIJKLMNOPQ-USD",
	} {
		t.Run("invalid "+in, func(t *testing.T) {
			_, err := ParseTicker(in)
			require.ErrorIs(t, err, ErrInvalidTicker)
		})
	}
}

func TestTickerString(t *testing.T) {
	assert.Equal(t, "BTC-USD", Ticker{Base: "BTC", Quote: "USD"}.String())
}
