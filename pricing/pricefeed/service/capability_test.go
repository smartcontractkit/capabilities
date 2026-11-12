package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/samber/do/v2"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/protos"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/protos/protostest"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/provider"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

var metadata = capabilities.RequestMetadata{WorkflowID: "workflow-id"}

// instances is how many instances the tests run: a node runs its capability alone.
const instances = 1

func start(t *testing.T, cfg Config, n int) protostest.ClientInstances {
	t.Helper()

	return capabilitytest.Start(t, New, n, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue(&cfg)
	}))
}

// coinbase serves the Coinbase Price API, pricing BTC-USD at price and nothing else.
func coinbase(t *testing.T, price string) Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/prices/BTC-USD/spot" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"errors":[{"id":"invalid_request","message":"Invalid currency"}]}`))
			return
		}

		_, _ = w.Write([]byte(`{"data":{"amount":"` + price + `","base":"BTC","currency":"USD"}}`))
	}))
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.Coinbase.URL = srv.URL
	return cfg
}

func TestNew(t *testing.T) {
	c := start(t, coinbase(t, "1"), 1)[0]

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, protos.ClientID, info.ID)
	assert.Equal(t, "price-feed@1.0.0-alpha", info.ID)
	assert.NotEmpty(t, info.Description)
	assert.NoError(t, c.Ready())
}

func TestNewInvalidConfig(t *testing.T) {
	for name, cfg := range map[string]func(*Config){
		"unknown provider":       func(c *Config) { c.Provider = "nope" },
		"invalid coinbase URL":   func(c *Config) { c.Coinbase.URL = "nope" },
		"data engine no secret":  func(c *Config) { c.Provider = ProviderDataEngine; c.DataEngine.APIKey = "key" },
		"data engine bad feeds":  func(c *Config) { c.Provider = ProviderDataEngine; c.DataEngine.Feeds = map[string]string{"x": "y"} },
		"data engine no API key": func(c *Config) { c.Provider = ProviderDataEngine; c.DataEngine.APISecret = "secret" },
	} {
		t.Run(name, func(t *testing.T) {
			c := DefaultConfig()
			cfg(&c)
			_, err := NewProvider(c)
			assert.Error(t, err)
		})
	}
}

func TestNewProviders(t *testing.T) {
	c := DefaultConfig()
	p, err := NewProvider(c)
	require.NoError(t, err)
	assert.Equal(t, ProviderCoinbase, p.Name())

	c.Provider = ProviderDataEngine
	c.DataEngine.APIKey = "key"
	c.DataEngine.APISecret = "secret"
	c.DataEngine.Feeds = map[string]string{"BTC-USD": "0x00039d9e45394f473ab1f050a1b963e6b05351e52d71e507509ada0c95ed75b8"}
	p, err = NewProvider(c)
	require.NoError(t, err)
	assert.Equal(t, ProviderDataEngine, p.Name())
}

func TestLifecycle(t *testing.T) {
	s := newService(logger.Test(t), DefaultConfig(), nil)
	require.Error(t, s.Ready())
	require.Error(t, s.HealthReport()[s.Name()])

	require.NoError(t, s.Start(t.Context()))
	require.NoError(t, s.Ready())
	require.NoError(t, s.HealthReport()[s.Name()])

	require.NoError(t, s.Close())
	require.Error(t, s.Ready())
	require.Error(t, s.HealthReport()[s.Name()])
}

func TestNewWithoutDependencies(t *testing.T) {
	_, err := New(do.New())
	assert.Error(t, err)
}

func TestGetPrice(t *testing.T) {
	c := start(t, coinbase(t, "89012.345678901234567891"), instances)
	before := time.Now()

	resp, err := c.GetPrice(t.Context(), metadata, &protos.GetPriceRequest{Ticker: "btc/usd"})
	require.NoError(t, err)
	assert.Equal(t, "BTC-USD", resp.Response.Ticker)

	price, err := values.FromProto(&pb.Value{Value: &pb.Value_DecimalValue{DecimalValue: resp.Response.Price}})
	require.NoError(t, err)
	got, err := price.Unwrap()
	require.NoError(t, err)
	assert.True(t, decimal.RequireFromString("89012.345678901234567891").Equal(got.(decimal.Decimal)), got)

	ts := resp.Response.Timestamp.AsTime()
	assert.False(t, ts.Before(before.Truncate(time.Second)), ts)
	assert.False(t, ts.After(time.Now()), ts)
}

func TestGetPriceErrors(t *testing.T) {
	c := start(t, coinbase(t, "-1"), instances)
	for name, tc := range map[string]struct {
		ticker string
		code   caperrors.ErrorCode
		origin caperrors.Origin
	}{
		"invalid ticker":     {"BTCUSD", caperrors.InvalidArgument, caperrors.OriginUser},
		"empty ticker":       {"", caperrors.InvalidArgument, caperrors.OriginUser},
		"unsupported ticker": {"FOO-BAR", caperrors.NotFound, caperrors.OriginUser},
		"bad price":          {"BTC-USD", caperrors.Unavailable, caperrors.OriginSystem},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := c.GetPrice(t.Context(), metadata, &protos.GetPriceRequest{Ticker: tc.ticker})
			var capErr caperrors.Error
			require.ErrorAs(t, err, &capErr)
			assert.Equal(t, tc.code, capErr.Code(), capErr)
			assert.Equal(t, tc.origin, capErr.Origin(), capErr)
		})
	}
}

// fakeProvider returns quote or err.
type fakeProvider struct {
	quote provider.Quote
	err   error
}

func (f fakeProvider) Name() string { return "fake" }

func (f fakeProvider) Price(ctx context.Context, _ provider.Ticker) (provider.Quote, error) {
	if f.err != nil {
		return provider.Quote{}, f.err
	}

	return f.quote, ctx.Err()
}

func TestGetPriceAge(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	cfg := DefaultConfig()
	cfg.MaxPriceAge = time.Minute
	for name, tc := range map[string]struct {
		age  time.Duration
		max  time.Duration
		code caperrors.ErrorCode
	}{
		"fresh":         {age: 30 * time.Second, max: time.Minute},
		"stale":         {age: 2 * time.Minute, max: time.Minute, code: caperrors.Unavailable},
		"any age":       {age: 24 * time.Hour},
		"small skew":    {age: -10 * time.Second, max: time.Minute},
		"in the future": {age: -time.Hour, max: time.Minute, code: caperrors.Unavailable},
	} {
		t.Run(name, func(t *testing.T) {
			cfg.MaxPriceAge = tc.max
			s := newService(logger.Test(t), cfg, fakeProvider{quote: provider.Quote{Price: decimal.NewFromInt(1), Timestamp: now.Add(-tc.age)}})
			s.now = func() time.Time { return now }

			resp, err := s.GetPrice(t.Context(), metadata, &protos.GetPriceRequest{Ticker: "BTC-USD"})
			if tc.code == 0 {
				require.NoError(t, err)
				assert.Equal(t, now.Add(-tc.age), resp.Response.Timestamp.AsTime())
				return
			}

			require.Error(t, err)
			assert.Equal(t, tc.code, err.Code(), err)
		})
	}
}

func TestToCapError(t *testing.T) {
	for name, tc := range map[string]struct {
		err        error
		code       caperrors.ErrorCode
		origin     caperrors.Origin
		visibility caperrors.Visibility
	}{
		"invalid":     {provider.ErrInvalidTicker, caperrors.InvalidArgument, caperrors.OriginUser, caperrors.VisibilityPublic},
		"unsupported": {provider.ErrUnsupportedTicker, caperrors.NotFound, caperrors.OriginUser, caperrors.VisibilityPublic},
		"timeout":     {context.DeadlineExceeded, caperrors.DeadlineExceeded, caperrors.OriginSystem, caperrors.VisibilityPublic},
		"canceled":    {context.Canceled, caperrors.Canceled, caperrors.OriginSystem, caperrors.VisibilityPublic},
		"bad":         {provider.ErrBadResponse, caperrors.Unavailable, caperrors.OriginSystem, caperrors.VisibilityPublic},
		"network":     {errors.New("dial tcp 10.0.0.1:443: connection refused"), caperrors.Unavailable, caperrors.OriginSystem, caperrors.VisibilityPrivate},
	} {
		t.Run(name, func(t *testing.T) {
			err := toCapError(tc.err)
			assert.Equal(t, tc.code, err.Code())
			assert.Equal(t, tc.origin, err.Origin())
			assert.Equal(t, tc.visibility, err.Visibility())
		})
	}
}

func TestGetPriceTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })

	cfg := DefaultConfig()
	cfg.Coinbase.URL = srv.URL
	cfg.Timeout = 50 * time.Millisecond
	p, err := NewProvider(cfg)
	require.NoError(t, err)

	_, capErr := newService(logger.Test(t), cfg, p).GetPrice(t.Context(), metadata, &protos.GetPriceRequest{Ticker: "BTC-USD"})
	require.Error(t, capErr)
	assert.Equal(t, caperrors.DeadlineExceeded, capErr.Code(), capErr)
}
