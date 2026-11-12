package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// DataEngineURL is the Chainlink Data Engine (Data Streams) REST API's mainnet URL.
const DataEngineURL = "https://api.dataengine.chain.link"

// dataEngineDecimals is how many decimals the prices of the report schemas DataEngine decodes have.
const dataEngineDecimals = 18

// DataEngine fetches the latest reports of Chainlink Data Engine (Data Streams) feeds over its REST API:
// https://docs.chain.link/data-streams/reference/data-streams-api/interface-api
type DataEngine struct {
	baseURL   *url.URL
	client    *http.Client
	apiKey    string
	apiSecret []byte
	// feedIDs are the hex IDs of the feeds, by the ticker they price.
	feedIDs map[Ticker]string
	now     func() time.Time
}

// NewDataEngine returns a DataEngine provider for the API at baseURL, such as DataEngineURL. It authenticates with
// apiKey and apiSecret, and prices the tickers of feedIDs, the hex IDs of feeds by their ticker, such as BTC-USD.
func NewDataEngine(baseURL, apiKey, apiSecret string, feedIDs map[string]string, client *http.Client) (*DataEngine, error) {
	u, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	if apiKey == "" || apiSecret == "" {
		return nil, fmt.Errorf("data engine needs an API key and secret")
	}

	ids := make(map[Ticker]string, len(feedIDs))
	for t, id := range feedIDs {
		ticker, err := ParseTicker(t)
		if err != nil {
			return nil, fmt.Errorf("feed of %q: %w", t, err)
		}

		raw, err := decodeHex(id)
		if err != nil || len(raw) != 32 {
			return nil, fmt.Errorf("feed of %s: invalid feed ID %q: must be 32 bytes of hex", ticker, id)
		}

		if _, ok := ids[ticker]; ok {
			return nil, fmt.Errorf("feed of %s is configured more than once", ticker)
		}

		ids[ticker] = "0x" + hex.EncodeToString(raw)
	}

	return &DataEngine{
		baseURL:   u,
		client:    client,
		apiKey:    apiKey,
		apiSecret: []byte(apiSecret),
		feedIDs:   ids,
		now:       time.Now,
	}, nil
}

func (d *DataEngine) Name() string { return "dataengine" }

type dataEngineResponse struct {
	Report *struct {
		FeedID                string `json:"feedID"`
		ObservationsTimestamp int64  `json:"observationsTimestamp"`
		FullReport            string `json:"fullReport"`
	} `json:"report"`
}

// Price returns the benchmark price of the latest report of ticker's feed, and the time it was observed.
func (d *DataEngine) Price(ctx context.Context, ticker Ticker) (Quote, error) {
	feedID, ok := d.feedIDs[ticker]
	if !ok {
		return Quote{}, fmt.Errorf("%w %s: no data engine feed is configured for it", ErrUnsupportedTicker, ticker)
	}

	u := d.baseURL.JoinPath("api", "v1", "reports", "latest")
	u.RawQuery = url.Values{"feedID": {feedID}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Quote{}, err
	}

	d.sign(req)
	status, body, err := get(d.client, req)
	if err != nil {
		return Quote{}, err
	}

	if status != http.StatusOK {
		return Quote{}, fmt.Errorf("%w: status %d: %s", ErrBadResponse, status, truncate(body))
	}

	var resp dataEngineResponse
	if err = json.Unmarshal(body, &resp); err != nil {
		return Quote{}, fmt.Errorf("%w: invalid JSON: %w", ErrBadResponse, err)
	}

	if resp.Report == nil {
		return Quote{}, fmt.Errorf("%w: no report", ErrBadResponse)
	}

	if !strings.EqualFold(resp.Report.FeedID, feedID) {
		return Quote{}, fmt.Errorf("%w: report is for feed %s, not %s", ErrBadResponse, resp.Report.FeedID, feedID)
	}

	fullReport, err := decodeHex(resp.Report.FullReport)
	if err != nil {
		return Quote{}, fmt.Errorf("%w: invalid full report: %w", ErrBadResponse, err)
	}

	r, err := decodeReport(fullReport)
	if err != nil {
		return Quote{}, fmt.Errorf("%w: %w", ErrBadResponse, err)
	}

	if "0x"+hex.EncodeToString(r.feedID[:]) != feedID {
		return Quote{}, fmt.Errorf("%w: signed report is for feed 0x%x, not %s", ErrBadResponse, r.feedID, feedID)
	}

	price := decimal.NewFromBigInt(r.price, -dataEngineDecimals)
	if err = validPrice(price); err != nil {
		return Quote{}, err
	}

	return Quote{Price: price, Timestamp: time.Unix(int64(r.observationsTimestamp), 0).UTC()}, nil
}

// sign adds the HMAC headers the Data Engine API authenticates requests with. The string signed is the method, the
// path and query, the SHA-256 of the body (empty for a GET), the API key and the timestamp in milliseconds.
func (d *DataEngine) sign(req *http.Request) {
	ts := strconv.FormatInt(d.now().UnixMilli(), 10)
	bodyHash := sha256.Sum256(nil)
	msg := strings.Join([]string{req.Method, req.URL.RequestURI(), hex.EncodeToString(bodyHash[:]), d.apiKey, ts}, " ")

	mac := hmac.New(sha256.New, d.apiSecret)
	mac.Write([]byte(msg))

	req.Header.Set("Authorization", d.apiKey)
	req.Header.Set("X-Authorization-Timestamp", ts)
	req.Header.Set("X-Authorization-Signature-SHA256", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Accept", "application/json")
}

// report is what DataEngine uses of a decoded report.
type report struct {
	feedID                [32]byte
	observationsTimestamp uint32
	price                 *big.Int
}

const word = 32

// Report schemas DataEngine decodes, which are the first two bytes of a feed ID. Each starts with feedId,
// validFromTimestamp, observationsTimestamp, nativeFee, linkFee and expiresAt, and has its price after them:
// benchmarkPrice for v2 and v3 (crypto), price for v4 (real world assets).
var priceSchemas = map[uint16]bool{2: true, 3: true, 4: true}

const (
	observationsTimestampWord = 2
	priceWord                 = 6
)

// decodeReport decodes the report blob of fullReport, which is the ABI encoding of
// (bytes32[3] reportContext, bytes reportBlob, bytes32[] rawRs, bytes32[] rawSs, bytes32 rawVs).
// It does not verify the signatures: the report is from the authenticated API, and verifying is done on chain.
func decodeReport(fullReport []byte) (report, error) {
	var r report
	blob, err := abiBytes(fullReport, 3)
	if err != nil {
		return r, fmt.Errorf("invalid full report: %w", err)
	}

	if len(blob) < (priceWord+1)*word {
		return r, fmt.Errorf("report blob is %d bytes, too short for a price", len(blob))
	}

	copy(r.feedID[:], blob[:word])
	schema := uint16(r.feedID[0])<<8 | uint16(r.feedID[1])
	if !priceSchemas[schema] {
		return r, fmt.Errorf("report schema v%d is not supported", schema)
	}

	ts, err := abiUint32(blob[observationsTimestampWord*word : (observationsTimestampWord+1)*word])
	if err != nil {
		return r, fmt.Errorf("invalid observationsTimestamp: %w", err)
	}

	r.observationsTimestamp = ts
	r.price = abiInt(blob[priceWord*word : (priceWord+1)*word])
	return r, nil
}

// abiBytes returns the dynamic bytes argument at index i of the ABI encoded data.
func abiBytes(data []byte, i int) ([]byte, error) {
	if len(data) < (i+1)*word {
		return nil, fmt.Errorf("%d bytes is too short", len(data))
	}

	offset, err := abiOffset(data[i*word : (i+1)*word])
	if err != nil {
		return nil, err
	}

	if offset > uint64(len(data)-word) { //nolint:gosec // len(data) >= word, checked above.
		return nil, fmt.Errorf("offset %d is out of range", offset)
	}

	length, err := abiOffset(data[offset : offset+word])
	if err != nil {
		return nil, err
	}

	start := offset + word
	if length > uint64(len(data))-start {
		return nil, fmt.Errorf("length %d is out of range", length)
	}

	return data[start : start+length], nil
}

// abiOffset decodes a uint256 that is an offset or a length, so must fit in an int.
func abiOffset(w []byte) (uint64, error) {
	v := new(big.Int).SetBytes(w)
	if !v.IsUint64() || v.Uint64() > 1<<32 {
		return 0, fmt.Errorf("offset or length %s is out of range", v)
	}

	return v.Uint64(), nil
}

// abiUint32 decodes a uint32 word.
func abiUint32(w []byte) (uint32, error) {
	v := new(big.Int).SetBytes(w)
	if !v.IsUint64() || v.Uint64() > 1<<32-1 {
		return 0, fmt.Errorf("%s does not fit in a uint32", v)
	}

	return uint32(v.Uint64()), nil //nolint:gosec // Checked to fit above.
}

// abiInt decodes a signed, two's complement int word, such as an int192, which ABI encoding sign extends to a word.
func abiInt(w []byte) *big.Int {
	v := new(big.Int).SetBytes(w)
	if w[0]&0x80 != 0 {
		v.Sub(v, new(big.Int).Lsh(big.NewInt(1), word*8))
	}

	return v
}

func decodeHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"))
}

// truncate returns the start of a response body, for errors.
func truncate(body []byte) string {
	const maxLen = 256
	body = bytes.TrimSpace(body)
	if len(body) > maxLen {
		return string(body[:maxLen]) + "..."
	}

	return string(body)
}
