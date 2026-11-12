# Price Feed Action

A node-mode action that returns the current price of a cryptocurrency pair. The node operator configures the price
provider and holds its credentials, so a workflow needs no API URL, API key or response parsing of its own.

Capability ID: `price-feed@1.0.0-alpha`

## Use from a workflow

Each node fetches its own price. The workflow gets consensus on the result, for example with the median:

```go
func fetchPrice(config *Config, _ *slog.Logger, nodeRuntime *pricefeed.NodeRuntime) (decimal.Decimal, error) {
	resp, err := (&pricefeed.Client{}).GetPrice(nodeRuntime, &pricefeed.GetPriceRequest{Ticker: config.Ticker}).Await()
	...
}

price, err := pricefeed.RunInNodeMode(config, runtime, fetchPrice, cre.ConsensusMedianAggregation()).Await()
```

[`workflow/`](workflow) is a complete example. It runs against the real capability with an in-process stand-in for
`cre-sdk-go` (`workflow/fakesdk`), until the SDK is generated from the production protos.

## Input: `GetPriceRequest`

| Field    | Type   | Description                                                                                     |
|----------|--------|-------------------------------------------------------------------------------------------------|
| `ticker` | string | `BASE-QUOTE`, such as `BTC-USD`. `/` or `_` can also separate the pair. The case does not matter. |

## Output: `GetPriceResponse`

| Field       | Type                        | Description                                                                         |
|-------------|-----------------------------|-------------------------------------------------------------------------------------|
| `ticker`    | string                      | The normalised pair, such as `BTC-USD`.                                             |
| `price`     | `values.v1.Decimal`         | The price of one unit of the base currency in the quote currency. No precision loss. |
| `timestamp` | `google.protobuf.Timestamp` | When the provider observed the price. Coinbase gives none, so it is the receive time. |

## Providers

| Provider     | Source                                                              | Timestamp                     | Credentials           |
|--------------|---------------------------------------------------------------------|-------------------------------|-----------------------|
| `coinbase`   | Coinbase Price API, `GET /v2/prices/{BASE-QUOTE}/spot`              | When the node got the price   | None                  |
| `dataengine` | Chainlink Data Engine REST API, `GET /api/v1/reports/latest?feedID` | Report `observationsTimestamp` | API key and secret (HMAC) |

Data Engine reports are decoded for the v2, v3 (crypto) and v4 (RWA) schemas, which have their price at the same
position, with 18 decimals. Each ticker must be mapped to a feed ID in `pricefeed.data-engine.feeds`. The report
signatures are not verified: the report comes from the authenticated API, and on-chain consumers verify it.

## Configuration

Set by flag, `CL_` environment variable or config file (`pricefeed run --help` lists them all):

| Name                                  | Default                            | Description                                                      |
|---------------------------------------|------------------------------------|------------------------------------------------------------------|
| `pricefeed.provider`                  | `coinbase`                         | `coinbase` or `dataengine`.                                      |
| `pricefeed.timeout`                   | `5s`                               | The longest a request to the provider can take.                  |
| `pricefeed.max-price-age`             | `5m`                               | Prices with an older timestamp are rejected. `0` accepts any age. |
| `pricefeed.coinbase.url`              | `https://api.coinbase.com`         | Coinbase API base URL.                                           |
| `pricefeed.data-engine.url`           | `https://api.dataengine.chain.link` | Data Engine REST API base URL.                                   |
| `pricefeed.data-engine.api-key`       |                                    | Data Engine API key.                                             |
| `pricefeed.data-engine.api-secret`    |                                    | Data Engine API secret. Redacted in logs.                        |
| `pricefeed.data-engine.feeds.<TICKER>` |                                    | The feed ID of a ticker, such as `BTC-USD=0x0003...`.            |

## Errors

| Cause                                        | Code               | Origin |
|----------------------------------------------|--------------------|--------|
| Ticker is not `BASE-QUOTE`                   | `InvalidArgument`  | User   |
| Provider has no price for the ticker         | `NotFound`         | User   |
| Provider timed out                           | `DeadlineExceeded` | System |
| Bad response: status, JSON, report, a price that is not positive, or a stale or future timestamp | `Unavailable` | System |
| Network failure (message kept private)       | `Unavailable`      | System |

## Not in scope

Historical prices (a non-goal of the PRD).
