package workflow

import (
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	nethttp "net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	httpcap "github.com/smartcontractkit/capabilities/http_action/action"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/integration_tests/http/workflow/fakesdk/cre"
	"github.com/smartcontractkit/capabilities/integration_tests/http/workflow/fakesdk/cron"
	"github.com/smartcontractkit/capabilities/integration_tests/http/workflow/fakesdk/http"
)

func TestFetchingDataWorkflow(t *testing.T) {
	// Stands in for api.mathjs.org: each node gets a different "random" number.
	nodeResults := []string{"7", "99", "42", "13"}
	var calls atomic.Int32
	api := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, _ *nethttp.Request) {
		_, _ = io.WriteString(w, nodeResults[int(calls.Add(1)-1)%len(nodeResults)])
	}))
	t.Cleanup(api.Close)

	lggr := logger.Test(t)
	httpAction := httpcap.NewService(lggr, limits.Factory{Logger: lggr, Meter: beholder.GetMeter()})
	require.NoError(t, httpAction.Initialise(t.Context(), core.StandardCapabilitiesDependencies{
		Config: fmt.Sprintf(`{"proxyMode":"direct","httpClient":{"allowedSchemes":["http"],"allowedIPs":["127.0.0.1"],"allowedPorts":[%d]}}`,
			api.Listener.Addr().(*net.TCPAddr).Port),
	}))
	t.Cleanup(func() { require.NoError(t, httpAction.Close()) })

	config, err := cre.ParseJSON[Config]([]byte(fmt.Sprintf(`{"schedule":"0 */1 * * * *","apiUrl":%q}`, api.URL)))
	require.NoError(t, err)

	wfLogger := slog.New(slog.NewTextHandler(t.Output(), nil))
	wf, err := InitWorkflow(config, wfLogger, nil)
	require.NoError(t, err)
	require.Len(t, wf, 1)

	runtime := &cre.TestRuntime{
		Ctx:  t.Context(),
		Lggr: wfLogger,
		RequestMeta: capabilities.RequestMetadata{
			WorkflowID:          "workflow_" + uuid.NewString(),
			WorkflowOwner:       "owner_" + uuid.NewString(),
			WorkflowExecutionID: "execution_" + uuid.NewString(),
		},
		Nodes:         len(nodeResults),
		CapabilityMap: map[string]any{http.CapabilityID: httpAction},
	}
	out, err := wf[0].Execute(config, runtime, &cron.Payload{})
	require.NoError(t, err)

	require.Equal(t, int32(len(nodeResults)), calls.Load())
	require.Equal(t, &MyResult{Result: big.NewInt(42)}, out)
}
