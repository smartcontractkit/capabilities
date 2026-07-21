package gateway

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/smartcontractkit/capabilities/http_action/common"
	"github.com/smartcontractkit/capabilities/http_action/validate"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/actions/http"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
)

const (
	gatewayDonUS = "gateway_don_us"
	gatewayDonEU = "gateway_don_eu"
)

func multiDonGatewayConnector(t *testing.T, onSend func(gatewayID string)) *mockGatewayConnector {
	t.Helper()
	return &mockGatewayConnector{
		Gateways: []mockGatewayEntry{
			{ID: "gateway_us_1", DonID: gatewayDonUS},
			{ID: "gateway_us_2", DonID: gatewayDonUS},
			{ID: "gateway_eu_1", DonID: gatewayDonEU},
			{ID: "gateway_eu_2", DonID: gatewayDonEU},
		},
		OnSend:    onSend,
		AwaitErrs: []error{nil},
	}
}

func newValidatorWithGatewayProxyDonID(t *testing.T, donID string) common.RequestValidator {
	t.Helper()

	getter, err := settings.NewJSONGetter(fmt.Appendf(nil, `{
		"org": {
			"test-org": {
				"PerWorkflow": {
					"HTTPAction": {
						"GatewayProxyDonID": %q
					}
				}
			}
		}
	}`, donID))
	require.NoError(t, err)

	lggr := logger.Test(t)
	validator, err := validate.NewValidator(lggr, limits.Factory{
		Logger:   lggr,
		Settings: getter,
	})
	require.NoError(t, err)
	return validator
}

func gatewayDonIDFor(connector *mockGatewayConnector, gatewayID string) string {
	for _, gw := range connector.Gateways {
		if gw.ID == gatewayID {
			return gw.DonID
		}
	}
	return ""
}

func TestGatewayOutboundProxy_SendRequest_routesToResolvedDonGateway(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		resolvedDonID     string
		eligibleGatewayID string
		excludedGatewayID string
	}{
		{
			name:              "routes to US gateway when GatewayProxyDonID resolves to gateway_don_us",
			resolvedDonID:     gatewayDonUS,
			eligibleGatewayID: "gateway_us_1",
			excludedGatewayID: "gateway_eu_1",
		},
		{
			name:              "routes to EU gateway when GatewayProxyDonID resolves to gateway_don_eu",
			resolvedDonID:     gatewayDonEU,
			eligibleGatewayID: "gateway_eu_2",
			excludedGatewayID: "gateway_us_2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := contexts.WithCRE(t.Context(), contexts.CRE{
				Org:      "test-org",
				Owner:    "test-owner",
				Workflow: "test-workflow",
			})

			readyCh := make(chan string, 1)
			mockConnector := multiDonGatewayConnector(t, func(id string) { readyCh <- id })

			validator := newValidatorWithGatewayProxyDonID(t, tt.resolvedDonID)
			resolvedDonID, err := validator.ResolveGatewayProxyDonID(ctx)
			require.NoError(t, err)
			require.Equal(t, tt.resolvedDonID, resolvedDonID)

			eligibleIDs, err := mockConnector.GatewayIDsForDon(ctx, resolvedDonID)
			require.NoError(t, err)
			require.Contains(t, eligibleIDs, tt.eligibleGatewayID)
			require.NotContains(t, eligibleIDs, tt.excludedGatewayID)

			proxy, err := NewGatewayOutboundProxy(
				mockConnector,
				common.ServiceConfig{},
				logger.Test(t),
				newMetrics(t),
				validator,
			)
			require.NoError(t, err)

			metadata := capabilities.RequestMetadata{
				WorkflowID:          "wf1",
				WorkflowExecutionID: "exec1",
				WorkflowOwner:       "owner1",
			}
			input := &http.Request{
				Url:           fmt.Sprintf("http://example.com/%s", tt.resolvedDonID),
				Method:        "GET",
				Timeout:       durationpb.New(5000 * time.Millisecond),
				CacheSettings: &http.CacheSettings{},
			}

			go func() {
				selectedGateway := <-readyCh
				simulateGatewayMessage(t, proxy, selectedGateway, 200, "ok", "", true)
			}()

			output, _, err := proxy.SendRequest(ctx, metadata, input, time.Now())
			require.NoError(t, err)
			require.NotNil(t, output)

			require.Len(t, mockConnector.awaitCalls, 1)
			selectedGateway := mockConnector.awaitCalls[0]
			require.Contains(t, eligibleIDs, selectedGateway)
			require.NotEqual(t, tt.excludedGatewayID, selectedGateway)
			require.Equal(t, tt.resolvedDonID, gatewayDonIDFor(mockConnector, selectedGateway))
		})
	}
}
