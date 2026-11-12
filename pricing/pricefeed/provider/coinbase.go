package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// CoinbaseURL is the Coinbase API's production URL.
const CoinbaseURL = "https://api.coinbase.com"

// Coinbase fetches spot prices from the Coinbase Price API, which needs no API key:
// https://docs.cdp.coinbase.com/coinbase-app/track-apis/prices
type Coinbase struct {
	baseURL *url.URL
	client  *http.Client
	now     func() time.Time
}

// NewCoinbase returns a Coinbase provider for the API at baseURL, such as CoinbaseURL.
func NewCoinbase(baseURL string, client *http.Client) (*Coinbase, error) {
	u, err := parseBaseURL(baseURL)
	if err != nil {
		return nil, err
	}

	return &Coinbase{baseURL: u, client: client, now: time.Now}, nil
}

func (c *Coinbase) Name() string { return "coinbase" }

type coinbaseResponse struct {
	Data *struct {
		Amount   string `json:"amount"`
		Base     string `json:"base"`
		Currency string `json:"currency"`
	} `json:"data"`
	Errors []struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"errors"`
	// Message is how some errors, such as an unknown pair, are given instead of Errors.
	Message string `json:"message"`
}

// Price returns the spot price of ticker. The Price API does not say when it observed the price, so its timestamp
// is when the response was received.
func (c *Coinbase) Price(ctx context.Context, ticker Ticker) (Quote, error) {
	u := c.baseURL.JoinPath("v2", "prices", ticker.String(), "spot")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return Quote{}, err
	}

	req.Header.Set("Accept", "application/json")
	status, body, err := get(c.client, req)
	if err != nil {
		return Quote{}, err
	}

	received := c.now()
	var resp coinbaseResponse
	if err = json.Unmarshal(body, &resp); err != nil {
		return Quote{}, fmt.Errorf("%w: status %d, invalid JSON: %w", ErrBadResponse, status, err)
	}

	switch {
	case status == http.StatusBadRequest || status == http.StatusNotFound:
		// Coinbase answers an unknown currency with 400 or 404, and an error saying which.
		return Quote{}, fmt.Errorf("%w %s: coinbase: %s", ErrUnsupportedTicker, ticker, coinbaseErrors(resp))
	case status != http.StatusOK:
		return Quote{}, fmt.Errorf("%w: status %d: %s", ErrBadResponse, status, coinbaseErrors(resp))
	case resp.Data == nil:
		return Quote{}, fmt.Errorf("%w: no data", ErrBadResponse)
	case !strings.EqualFold(resp.Data.Base, ticker.Base) || !strings.EqualFold(resp.Data.Currency, ticker.Quote):
		return Quote{}, fmt.Errorf("%w: price is for %s-%s, not %s", ErrBadResponse, resp.Data.Base, resp.Data.Currency, ticker)
	}

	price, err := decimal.NewFromString(resp.Data.Amount)
	if err != nil {
		return Quote{}, fmt.Errorf("%w: invalid amount %q: %w", ErrBadResponse, resp.Data.Amount, err)
	}

	if err = validPrice(price); err != nil {
		return Quote{}, err
	}

	return Quote{Price: price, Timestamp: received.UTC()}, nil
}

func coinbaseErrors(resp coinbaseResponse) string {
	if len(resp.Errors) == 0 {
		if resp.Message != "" {
			return resp.Message
		}

		return "no error message"
	}

	msgs := make([]string, len(resp.Errors))
	for i, e := range resp.Errors {
		msgs[i] = e.ID + ": " + e.Message
	}

	return strings.Join(msgs, "; ")
}

// parseBaseURL parses the base URL of a provider's API, which must be absolute and http or https.
func parseBaseURL(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", s, err)
	}

	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("invalid URL %q: must be an absolute http or https URL", s)
	}

	return u, nil
}
