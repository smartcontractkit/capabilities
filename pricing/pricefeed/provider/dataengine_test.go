package provider

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testAPIKey    = "test-key"
	testAPISecret = "test-secret"
	// v3 (crypto) feed IDs start with 0x0003.
	btcFeedID = "0x00039d9e45394f473ab1f050a1b963e6b05351e52d71e507509ada0c95ed75b8"
	ethFeedID = "0x000362205e10b3a147d02792eccee483dca6c7b44ecce7012cb8c6e0b68b3ae9"
)

var observed = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

// u256 encodes v as an ABI word, two's complement when negative.
func u256(v *big.Int) []byte {
	if v.Sign() < 0 {
		v = new(big.Int).Add(v, new(big.Int).Lsh(big.NewInt(1), 256))
	}

	return v.FillBytes(make([]byte, word))
}

// reportBlob encodes a v2/v3/v4 report blob: feedId, validFromTimestamp, observationsTimestamp, nativeFee, linkFee,
// expiresAt, price, then extra.
func reportBlob(feedID string, ts uint32, price *big.Int, extra int) []byte {
	id, _ := decodeHex(feedID)
	blob := append([]byte{}, id...)
	for _, v := range []*big.Int{big.NewInt(int64(ts)), big.NewInt(int64(ts)), big.NewInt(1), big.NewInt(2), big.NewInt(int64(ts) + 86400), price} {
		blob = append(blob, u256(v)...)
	}

	for range extra {
		blob = append(blob, u256(price)...)
	}

	return blob
}

// fullReport ABI encodes (bytes32[3] reportContext, bytes reportBlob, bytes32[] rawRs, bytes32[] rawSs, bytes32 rawVs)
// with one signature.
func fullReport(blob []byte) []byte {
	head := 7 * word
	padded := (len(blob) + word - 1) / word * word
	blobOffset := head
	rsOffset := blobOffset + word + padded
	ssOffset := rsOffset + 2*word

	var out []byte
	for range 3 {
		out = append(out, make([]byte, word)...)
	}

	for _, o := range []int{blobOffset, rsOffset, ssOffset} {
		out = append(out, u256(big.NewInt(int64(o)))...)
	}

	out = append(out, make([]byte, word)...) // rawVs
	out = append(out, u256(big.NewInt(int64(len(blob))))...)
	out = append(out, blob...)
	out = append(out, make([]byte, padded-len(blob))...)
	for range 2 { // rs and ss, one signature each
		out = append(out, u256(big.NewInt(1))...)
		out = append(out, make([]byte, word)...)
	}

	return out
}

func reportResponse(feedID string, full []byte) string {
	b, _ := json.Marshal(map[string]any{"report": map[string]any{
		"feedID":                feedID,
		"validFromTimestamp":    observed.Unix(),
		"observationsTimestamp": observed.Unix(),
		"fullReport":            "0x" + hex.EncodeToString(full),
	}})
	return string(b)
}

func e18(s string) *big.Int {
	return decimal.RequireFromString(s).Shift(dataEngineDecimals).BigInt()
}

func newDataEngine(t *testing.T, handler http.HandlerFunc) *DataEngine {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	d, err := NewDataEngine(srv.URL, testAPIKey, testAPISecret, map[string]string{"BTC-USD": btcFeedID, "eth/usd": ethFeedID}, srv.Client())
	require.NoError(t, err)
	return d
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func TestDataEnginePrice(t *testing.T) {
	now := time.Now()
	d := newDataEngine(t, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/api/v1/reports/latest", r.URL.Path)
		assert.Equal(t, btcFeedID, r.URL.Query().Get("feedID"))

		// The request is signed as the Data Engine API documents.
		assert.Equal(t, testAPIKey, r.Header.Get("Authorization"))
		ts := r.Header.Get("X-Authorization-Timestamp")
		assert.Equal(t, strconv.FormatInt(now.UnixMilli(), 10), ts)
		emptyHash := sha256.Sum256(nil)
		mac := hmac.New(sha256.New, []byte(testAPISecret))
		mac.Write([]byte("GET " + r.URL.RequestURI() + " " + hex.EncodeToString(emptyHash[:]) + " " + testAPIKey + " " + ts))
		assert.Equal(t, hex.EncodeToString(mac.Sum(nil)), r.Header.Get("X-Authorization-Signature-SHA256"))

		_, _ = w.Write([]byte(reportResponse(btcFeedID, fullReport(reportBlob(btcFeedID, uint32(observed.Unix()), e18("89012.345678901234567891"), 2))))) //nolint:gosec // A test timestamp.
	})
	d.now = func() time.Time { return now }

	q, err := d.Price(t.Context(), btcUSD)
	require.NoError(t, err)
	assert.True(t, decimal.RequireFromString("89012.345678901234567891").Equal(q.Price), q.Price.String())
	assert.Equal(t, observed, q.Timestamp)
	assert.Equal(t, "dataengine", d.Name())
}

func TestDataEngineSchemas(t *testing.T) {
	for schema, extra := range map[string]int{"0002": 0, "0003": 2, "0004": 1} {
		t.Run("v"+schema, func(t *testing.T) {
			feedID := "0x" + schema + strings.Repeat("ab", 30)
			srv := httptest.NewServer(respond(http.StatusOK, reportResponse(feedID, fullReport(reportBlob(feedID, uint32(observed.Unix()), e18("1.5"), extra))))) //nolint:gosec // A test timestamp.
			t.Cleanup(srv.Close)
			d, err := NewDataEngine(srv.URL, testAPIKey, testAPISecret, map[string]string{"XAU-USD": feedID}, srv.Client())
			require.NoError(t, err)

			q, err := d.Price(t.Context(), Ticker{Base: "XAU", Quote: "USD"})
			require.NoError(t, err)
			assert.Equal(t, "1.5", q.Price.String())
		})
	}
}

func TestDataEngineErrors(t *testing.T) {
	ts := uint32(observed.Unix()) //nolint:gosec // A test timestamp.
	v8FeedID := "0x0008" + strings.Repeat("ab", 30)
	full := fullReport(reportBlob(btcFeedID, ts, e18("1"), 2))
	for name, tc := range map[string]struct {
		handler http.HandlerFunc
		msg     string
	}{
		"unauthorized":      {respond(http.StatusUnauthorized, `{"error":"bad signature"}`), "status 401"},
		"server error":      {respond(http.StatusInternalServerError, `oops`), "status 500"},
		"not JSON":          {respond(http.StatusOK, `<html>`), "invalid JSON"},
		"no report":         {respond(http.StatusOK, `{}`), "no report"},
		"other feed":        {respond(http.StatusOK, reportResponse(ethFeedID, full)), "not " + btcFeedID},
		"signed other feed": {respond(http.StatusOK, reportResponse(btcFeedID, fullReport(reportBlob(ethFeedID, ts, e18("1"), 2)))), "signed report is for feed"},
		"not hex":           {respond(http.StatusOK, strings.Replace(reportResponse(btcFeedID, full), `"fullReport":"0x00`, `"fullReport":"0xzz`, 1)), "invalid full report"},
		"truncated":         {respond(http.StatusOK, reportResponse(btcFeedID, full[:5*word])), "out of range"},
		"bad offset":        {respond(http.StatusOK, reportResponse(btcFeedID, append(append(append([]byte{}, full[:3*word]...), u256(big.NewInt(1<<20))...), full[4*word:]...))), "out of range"},
		"short blob":        {respond(http.StatusOK, reportResponse(btcFeedID, fullReport(reportBlob(btcFeedID, ts, e18("1"), 0)[:4*word]))), "too short for a price"},
		"unknown schema":    {respond(http.StatusOK, reportResponse(btcFeedID, fullReport(reportBlob(v8FeedID, ts, e18("1"), 2)))), "schema v8"},
		"zero price":        {respond(http.StatusOK, reportResponse(btcFeedID, fullReport(reportBlob(btcFeedID, ts, big.NewInt(0), 2)))), "not positive"},
		"negative price":    {respond(http.StatusOK, reportResponse(btcFeedID, fullReport(reportBlob(btcFeedID, ts, e18("-2"), 2)))), "-2 is not positive"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newDataEngine(t, tc.handler).Price(t.Context(), btcUSD)
			require.ErrorIs(t, err, ErrBadResponse)
			assert.ErrorContains(t, err, tc.msg)
		})
	}
}

func TestDataEngineUnsupportedTicker(t *testing.T) {
	d := newDataEngine(t, func(http.ResponseWriter, *http.Request) {
		t.Error("the API must not be called for a ticker without a feed")
	})

	_, err := d.Price(t.Context(), Ticker{Base: "DOGE", Quote: "USD"})
	require.ErrorIs(t, err, ErrUnsupportedTicker)
}

func TestNewDataEngineInvalidConfig(t *testing.T) {
	feeds := map[string]string{"BTC-USD": btcFeedID}
	for name, tc := range map[string]struct {
		url, key, secret string
		feeds            map[string]string
	}{
		"invalid URL":     {"nope", testAPIKey, testAPISecret, feeds},
		"no key":          {DataEngineURL, "", testAPISecret, feeds},
		"no secret":       {DataEngineURL, testAPIKey, "", feeds},
		"invalid ticker":  {DataEngineURL, testAPIKey, testAPISecret, map[string]string{"BTCUSD": btcFeedID}},
		"invalid feed ID": {DataEngineURL, testAPIKey, testAPISecret, map[string]string{"BTC-USD": "0x1234"}},
		"not hex":         {DataEngineURL, testAPIKey, testAPISecret, map[string]string{"BTC-USD": "0xzz"}},
		"duplicate":       {DataEngineURL, testAPIKey, testAPISecret, map[string]string{"BTC-USD": btcFeedID, "btc/usd": btcFeedID}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewDataEngine(tc.url, tc.key, tc.secret, tc.feeds, http.DefaultClient)
			assert.Error(t, err)
		})
	}
}

func TestAbiInt(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 1 << 40, -(1 << 40)} {
		assert.Equal(t, v, abiInt(u256(big.NewInt(v))).Int64())
	}
}
