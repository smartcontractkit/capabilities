package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var btcUSD = Ticker{Base: "BTC", Quote: "USD"}

func newCoinbase(t *testing.T, status int, body string) *Coinbase {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/v2/prices/BTC-USD/spot", r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c, err := NewCoinbase(srv.URL, srv.Client())
	require.NoError(t, err)
	return c
}

func TestCoinbasePrice(t *testing.T) {
	c := newCoinbase(t, http.StatusOK, `{"data":{"amount":"89012.345678901234","base":"BTC","currency":"USD"}}`)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }

	q, err := c.Price(t.Context(), btcUSD)
	require.NoError(t, err)
	assert.True(t, decimal.RequireFromString("89012.345678901234").Equal(q.Price), q.Price.String())
	assert.Equal(t, now, q.Timestamp)
	assert.Equal(t, "coinbase", c.Name())
}

func TestCoinbaseErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		err    error
		msg    string
	}{
		"unknown currency": {http.StatusBadRequest, `{"errors":[{"id":"invalid_request","message":"Invalid base currency"}]}`, ErrUnsupportedTicker, "Invalid base currency"},
		"not found":        {http.StatusNotFound, `{"errors":[{"id":"not_found","message":"Not found"}]}`, ErrUnsupportedTicker, "not_found"},
		"unknown pair":     {http.StatusNotFound, `{"error":"rpc error: code = NotFound desc = not found","code":5,"message":"rpc error: code = NotFound desc = not found"}`, ErrUnsupportedTicker, "NotFound"},
		"server error":     {http.StatusInternalServerError, `{"errors":[{"id":"internal","message":"boom"}]}`, ErrBadResponse, "status 500"},
		"rate limited":     {http.StatusTooManyRequests, `{}`, ErrBadResponse, "no error message"},
		"not JSON":         {http.StatusOK, `<html>`, ErrBadResponse, "invalid JSON"},
		"no data":          {http.StatusOK, `{}`, ErrBadResponse, "no data"},
		"wrong pair":       {http.StatusOK, `{"data":{"amount":"1","base":"ETH","currency":"USD"}}`, ErrBadResponse, "not BTC-USD"},
		"not a number":     {http.StatusOK, `{"data":{"amount":"lots","base":"BTC","currency":"USD"}}`, ErrBadResponse, "invalid amount"},
		"empty amount":     {http.StatusOK, `{"data":{"amount":"","base":"BTC","currency":"USD"}}`, ErrBadResponse, "invalid amount"},
		"zero":             {http.StatusOK, `{"data":{"amount":"0","base":"BTC","currency":"USD"}}`, ErrBadResponse, "not positive"},
		"negative":         {http.StatusOK, `{"data":{"amount":"-1.5","base":"BTC","currency":"USD"}}`, ErrBadResponse, "not positive"},
		"too large":        {http.StatusOK, `{"data":{"amount":"1","pad":"` + strings.Repeat("x", maxBodySize) + `"}}`, ErrBadResponse, "larger than"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newCoinbase(t, tc.status, tc.body).Price(t.Context(), btcUSD)
			require.ErrorIs(t, err, tc.err)
			assert.ErrorContains(t, err, tc.msg)
		})
	}
}

func TestCoinbaseUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	c, err := NewCoinbase(srv.URL, srv.Client())
	require.NoError(t, err)

	_, err = c.Price(t.Context(), btcUSD)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrBadResponse)
	assert.NotErrorIs(t, err, ErrUnsupportedTicker)
}

func TestNewCoinbaseInvalidURL(t *testing.T) {
	for _, u := range []string{"", "api.coinbase.com", "ftp://api.coinbase.com", "://"} {
		_, err := NewCoinbase(u, http.DefaultClient)
		assert.Error(t, err, u)
	}
}
