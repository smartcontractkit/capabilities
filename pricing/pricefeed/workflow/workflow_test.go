package workflow

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/protos/protostest"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/service"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/workflow/fakesdk/cre"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/workflow/fakesdk/cron"
	"github.com/smartcontractkit/capabilities/pricing/pricefeed/workflow/fakesdk/pricefeed"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// run runs the workflow on len(bodies) nodes, each fetching its price from a Coinbase that answers with the next of bodies.
func run(t *testing.T, ticker string, bodies ...string) (*Result, int32, error) {
	t.Helper()
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, bodies[int(calls.Add(1)-1)%len(bodies)])
	}))
	t.Cleanup(api.Close)

	cfg := service.DefaultConfig()
	cfg.Coinbase.URL = api.URL
	priceFeed := protostest.ClientInstances(capabilitytest.Start(t, service.New, 1, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue(&cfg)
	})))

	config, err := cre.ParseJSON[Config]([]byte(`{"schedule":"0 */1 * * * *","ticker":"` + ticker + `"}`))
	require.NoError(t, err)

	lggr := slog.New(slog.NewTextHandler(t.Output(), nil))
	wf, err := InitWorkflow(config, lggr)
	require.NoError(t, err)
	require.Len(t, wf, 1)

	runtime := &cre.TestRuntime{
		Ctx:           t.Context(),
		Lggr:          lggr,
		RequestMeta:   capabilities.RequestMetadata{WorkflowID: "workflow-id", WorkflowExecutionID: "execution-id"},
		Nodes:         len(bodies),
		CapabilityMap: map[string]any{pricefeed.CapabilityID: priceFeed},
	}

	out, err := wf[0].Execute(config, runtime, &cron.Payload{})
	if err != nil {
		return nil, calls.Load(), err
	}

	return out.(*Result), calls.Load(), nil
}

func coinbasePrice(amount string) string {
	return `{"data":{"amount":"` + amount + `","base":"BTC","currency":"USD"}}`
}

func TestPriceFeedWorkflow(t *testing.T) {
	// Each node sees a slightly different price; the workflow agrees on their median.
	out, calls, err := run(t, "btc-usd",
		coinbasePrice("89000.10"), coinbasePrice("89000.30"), coinbasePrice("89000.20"), coinbasePrice("89000.40"))
	require.NoError(t, err)
	assert.Equal(t, int32(4), calls)
	assert.True(t, decimal.RequireFromString("89000.30").Equal(out.Price), out.Price)
}

func TestPriceFeedWorkflowBadTicker(t *testing.T) {
	_, calls, err := run(t, "not a ticker", coinbasePrice("1"))
	require.ErrorContains(t, err, "invalid ticker")
	assert.Zero(t, calls, "an invalid ticker must not reach the provider")
}

func TestPriceFeedWorkflowBadResponse(t *testing.T) {
	_, _, err := run(t, "BTC-USD", coinbasePrice("1"), `{"data":{"amount":"NaN","base":"BTC","currency":"USD"}}`)
	require.ErrorContains(t, err, "node 1")
	require.ErrorContains(t, err, "invalid amount")
}
