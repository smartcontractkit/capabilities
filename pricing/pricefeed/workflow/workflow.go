// Package workflow is an example workflow that uses the price feed capability, the "Desired" flow of the PRD: it asks
// for a ticker's price with no API URL, API key or response parsing of its own. It is built against fakesdk instead of
// cre-sdk-go, so it runs natively instead of as WASM.
package workflow

import (
	"fmt"
	"log/slog"

	"github.com/shopspring/decimal"

	"github.com/smartcontractkit/capabilities/pricing/pricefeed/workflow/fakesdk/cre"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/workflow/fakesdk/cron"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/workflow/fakesdk/pricefeed"

	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

type Config struct {
	Schedule string `json:"schedule"`
	Ticker   string `json:"ticker"`
}

type Result struct {
	Ticker string
	Price  decimal.Decimal
}

func InitWorkflow(config *Config, _ *slog.Logger) (cre.Workflow[*Config], error) {
	return cre.Workflow[*Config]{
		cre.Handler(cron.Trigger(&cron.Config{Schedule: config.Schedule}), onCronTrigger),
	}, nil
}

// fetchPrice runs on each node: it is all a workflow needs to get a price.
func fetchPrice(config *Config, _ *slog.Logger, nodeRuntime *pricefeed.NodeRuntime) (decimal.Decimal, error) {
	priceFeed := &pricefeed.Client{}
	resp, err := priceFeed.GetPrice(nodeRuntime, &pricefeed.GetPriceRequest{Ticker: config.Ticker}).Await()
	if err != nil {
		return decimal.Decimal{}, err
	}

	return toDecimal(resp.Price)
}

func onCronTrigger(config *Config, runtime cre.Runtime, _ *cron.Payload) (*Result, error) {
	// Each node fetches its own price, so the DON agrees on their median.
	price, err := pricefeed.RunInNodeMode(config, runtime, fetchPrice, cre.ConsensusMedianAggregation()).Await()
	if err != nil {
		return nil, err
	}

	runtime.Logger().Info("Fetched price", "ticker", config.Ticker, "price", price)
	return &Result{Ticker: config.Ticker, Price: price}, nil
}

func toDecimal(d *pb.Decimal) (decimal.Decimal, error) {
	v, err := values.FromProto(&pb.Value{Value: &pb.Value_DecimalValue{DecimalValue: d}})
	if err != nil {
		return decimal.Decimal{}, err
	}

	var price decimal.Decimal
	if err = v.UnwrapTo(&price); err != nil {
		return decimal.Decimal{}, fmt.Errorf("price is not a decimal: %w", err)
	}

	return price, nil
}
