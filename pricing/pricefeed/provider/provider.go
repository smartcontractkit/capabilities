// Package provider fetches current prices from the price providers the price feed capability supports.
package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Provider fetches the current price of a ticker.
type Provider interface {
	// Name is the provider's name, for logs and errors.
	Name() string
	// Price returns the current price of ticker. Its errors wrap ErrUnsupportedTicker when the provider has no price
	// for ticker, and ErrBadResponse when the provider answered with something that is not a valid price.
	Price(ctx context.Context, ticker Ticker) (Quote, error)
}

// Quote is a price and when it was observed.
type Quote struct {
	Price     decimal.Decimal
	Timestamp time.Time
}

var (
	// ErrInvalidTicker is returned for a ticker that is not a BASE-QUOTE pair. It is the caller's fault.
	ErrInvalidTicker = errors.New("invalid ticker")
	// ErrUnsupportedTicker is returned for a well formed ticker the provider has no price for. It is the caller's
	// fault.
	ErrUnsupportedTicker = errors.New("unsupported ticker")
	// ErrBadResponse is returned when the provider answers with something that is not a valid, current price. It is
	// not the caller's fault.
	ErrBadResponse = errors.New("bad response from price provider")
)

// Ticker is a normalised BASE-QUOTE pair, such as BTC-USD.
type Ticker struct {
	Base  string
	Quote string
}

func (t Ticker) String() string { return t.Base + "-" + t.Quote }

var tickerPattern = regexp.MustCompile(`^([A-Z0-9]{1,16})[-/_]([A-Z0-9]{1,16})$`)

// ParseTicker parses a BASE-QUOTE pair, such as BTC-USD, btc/usd or ETH_USDC. Surrounding space is ignored.
func ParseTicker(s string) (Ticker, error) {
	m := tickerPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
	if m == nil {
		return Ticker{}, fmt.Errorf("%w %q: must be BASE-QUOTE, such as BTC-USD", ErrInvalidTicker, s)
	}

	if m[1] == m[2] {
		return Ticker{}, fmt.Errorf("%w %q: base and quote are the same", ErrInvalidTicker, s)
	}

	return Ticker{Base: m[1], Quote: m[2]}, nil
}

// maxBodySize is the most a provider's response body is read, so a misbehaving provider cannot exhaust memory.
const maxBodySize = 1 << 20

// get sends req and returns the status code and body of the response.
func get(client *http.Client, req *http.Request) (int, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodySize+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("%w: failed to read body: %w", ErrBadResponse, err)
	}

	if len(body) > maxBodySize {
		return resp.StatusCode, nil, fmt.Errorf("%w: body is larger than %d bytes", ErrBadResponse, maxBodySize)
	}

	return resp.StatusCode, body, nil
}

// validPrice returns an error unless price is a usable price: more than zero.
func validPrice(price decimal.Decimal) error {
	if !price.IsPositive() {
		return fmt.Errorf("%w: price %s is not positive", ErrBadResponse, price)
	}

	return nil
}
