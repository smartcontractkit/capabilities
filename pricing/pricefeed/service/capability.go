package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/protos"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/provider"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	commonconfig "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConfigNamespace is the namespace the price feed capability's Config is set under.
const ConfigNamespace = "pricefeed"

// Providers the node operator can configure.
const (
	ProviderCoinbase   = "coinbase"
	ProviderDataEngine = "dataengine"
)

// Config is how the node operator configures the price feed capability. The operator, not the workflow, holds the
// provider's credentials, so workflows need no API keys of their own.
type Config struct {
	// Provider is the price provider prices are fetched from: coinbase or dataengine.
	Provider string `validate:"oneof=coinbase dataengine"`

	// Timeout is the longest a request to the provider can take.
	Timeout time.Duration `validate:"gt=0"`

	// MaxPriceAge is the oldest a price can be, by its timestamp, to be returned. Zero accepts any age.
	MaxPriceAge time.Duration `validate:"gte=0"`

	// Coinbase configures the coinbase provider.
	Coinbase CoinbaseConfig

	// DataEngine configures the dataengine provider.
	DataEngine DataEngineConfig
}

// CoinbaseConfig configures the Coinbase Price API provider, which needs no API key.
type CoinbaseConfig struct {
	// URL is the base URL of the Coinbase API.
	URL string
}

// DataEngineConfig configures the Chainlink Data Engine (Data Streams) provider.
type DataEngineConfig struct {
	// URL is the base URL of the Data Engine REST API.
	URL string

	// APIKey is the Data Engine API key.
	APIKey string

	// APISecret is the Data Engine API secret the requests are signed with.
	APISecret commonconfig.SecretString

	// Feeds are the hex IDs of the Data Engine feeds, by the ticker they price, such as BTC-USD. A ticker without a
	// feed is not supported.
	Feeds map[string]string
}

// DefaultConfig returns the config a node runs with when it sets nothing: Coinbase, which needs no credentials.
func DefaultConfig() Config {
	return Config{
		Provider:    ProviderCoinbase,
		Timeout:     5 * time.Second,
		MaxPriceAge: 5 * time.Minute,
		Coinbase:    CoinbaseConfig{URL: provider.CoinbaseURL},
		DataEngine:  DataEngineConfig{URL: provider.DataEngineURL},
	}
}

// init provides Config however the capability is run, so whoever embeds it gets its defaults without providing it.
func init() {
	cfg := DefaultConfig()
	capability.Injectors.MustProvideConfig(ConfigNamespace, &cfg)
}

var _ capability.Capability = (*protos.ClientServer)(nil)

// New builds the price feed capability from what the run provides, for capability.Run.
func New(i do.Injector) (*protos.ClientServer, error) {
	lggr, err := do.Invoke[logger.Logger](i)
	if err != nil {
		return nil, err
	}

	cfg, err := do.Invoke[*Config](i)
	if err != nil {
		return nil, err
	}

	p, err := NewProvider(*cfg)
	if err != nil {
		return nil, err
	}

	return protos.NewClientServer(newService(lggr, *cfg, p)), nil
}

// NewProvider returns the provider cfg configures.
func NewProvider(cfg Config) (provider.Provider, error) {
	client := &http.Client{Timeout: cfg.Timeout}
	switch cfg.Provider {
	case ProviderCoinbase:
		return provider.NewCoinbase(cfg.Coinbase.URL, client)
	case ProviderDataEngine:
		de := cfg.DataEngine
		return provider.NewDataEngine(de.URL, de.APIKey, string(de.APISecret), de.Feeds, client)
	default:
		return nil, fmt.Errorf("unknown price provider %q", cfg.Provider)
	}
}

// service implements the price feed capability.
type service struct {
	services.Service
	lggr     logger.SugaredLogger
	cfg      Config
	provider provider.Provider
	now      func() time.Time
}

var _ protos.ClientCapability = (*service)(nil)

func newService(lggr logger.Logger, cfg Config, p provider.Provider) *service {
	s := &service{cfg: cfg, provider: p, now: time.Now}
	s.Service = services.Config{Name: "PriceFeed"}.NewService(lggr)
	s.lggr = logger.Sugared(logger.Named(lggr, "PriceFeed"))
	return s
}

func (s *service) Description() string {
	return "Fetches current cryptocurrency prices from a provider the node operator configures"
}

// GetPrice validates the ticker, fetches its price from the provider and validates it.
func (s *service) GetPrice(ctx context.Context, metadata capabilities.RequestMetadata, input *protos.GetPriceRequest) (*capabilities.ResponseAndMetadata[*protos.GetPriceResponse], caperrors.Error) {
	lggr := logger.With(s.lggr, "workflowID", metadata.WorkflowID, "workflowExecutionID", metadata.WorkflowExecutionID,
		"provider", s.provider.Name())

	ticker, err := provider.ParseTicker(input.GetTicker())
	if err != nil {
		return nil, caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	}

	lggr = logger.With(lggr, "ticker", ticker.String())
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()

	quote, err := s.provider.Price(ctx, ticker)
	if err != nil {
		lggr.Warnw("Failed to fetch price", "err", err)
		return nil, toCapError(fmt.Errorf("failed to fetch the price of %s from %s: %w", ticker, s.provider.Name(), err))
	}

	if err = s.checkAge(quote); err != nil {
		lggr.Warnw("Price is stale", "err", err)
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("price of %s from %s: %w", ticker, s.provider.Name(), err),
			caperrors.Unavailable)
	}

	lggr.Debugw("Fetched price", "price", quote.Price.String(), "timestamp", quote.Timestamp)
	return &capabilities.ResponseAndMetadata[*protos.GetPriceResponse]{Response: &protos.GetPriceResponse{
		Ticker:    ticker.String(),
		Price:     pb.NewDecimalValue(quote.Price).GetDecimalValue(),
		Timestamp: timestamppb.New(quote.Timestamp),
	}}, nil
}

// clockSkew is how far in the future a price's timestamp can be, for clocks that differ a little.
const clockSkew = 30 * time.Second

func (s *service) checkAge(quote provider.Quote) error {
	now := s.now()
	if quote.Timestamp.After(now.Add(clockSkew)) {
		return fmt.Errorf("%w: timestamp %s is in the future", provider.ErrBadResponse, quote.Timestamp)
	}

	if s.cfg.MaxPriceAge > 0 {
		if age := now.Sub(quote.Timestamp); age > s.cfg.MaxPriceAge {
			return fmt.Errorf("price is %s old, older than the maximum of %s", age.Round(time.Second), s.cfg.MaxPriceAge)
		}
	}

	return nil
}

// toCapError tells the workflow's errors, which are its fault, apart from the provider's.
func toCapError(err error) caperrors.Error {
	switch {
	case errors.Is(err, provider.ErrInvalidTicker):
		return caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
	case errors.Is(err, provider.ErrUnsupportedTicker):
		return caperrors.NewPublicUserError(err, caperrors.NotFound)
	case errors.Is(err, context.DeadlineExceeded), isTimeout(err):
		return caperrors.NewPublicSystemError(err, caperrors.DeadlineExceeded)
	case errors.Is(err, context.Canceled):
		return caperrors.NewPublicSystemError(err, caperrors.Canceled)
	case errors.Is(err, provider.ErrBadResponse):
		return caperrors.NewPublicSystemError(err, caperrors.Unavailable)
	default:
		// Such as a failed connection, whose message can say more about the node's network than is public.
		return caperrors.NewPrivateSystemError(err, caperrors.Unavailable)
	}
}

// isTimeout tells whether err is a network timeout, such as the HTTP client's.
func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
