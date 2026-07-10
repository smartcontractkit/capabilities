package monitoring

import (
	"errors"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/stretchr/testify/require"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
)

func TestNs(t *testing.T) {
	t.Parallel()
	require.Equal(t, "stellar_capability_read_contract_success", ns("read_contract_success"))
}

func TestNewMetrics_ConstructorError(t *testing.T) {
	orig := newMetricsCapBasic
	defer func() { newMetricsCapBasic = orig }()

	newMetricsCapBasic = func(capmonitoring.MetricsInfoCapBasic) (capmonitoring.MetricsCapBasic, error) {
		return capmonitoring.MetricsCapBasic{}, errors.New("instrument registration failed")
	}

	_, err := NewMetrics()
	require.Error(t, err)
	require.Contains(t, err.Error(), "instrument registration failed")
}

func TestMetricViews_CapDurationBuckets(t *testing.T) {
	t.Parallel()

	views := MetricViews()
	expectedBuckets := []float64{
		10, 25, 50, 100, 250, 500,
		1000, 2500, 5000,
		10000, 20000, 30000, 60000,
	}
	expectedMetricNames := []string{
		"stellar_capability_read_contract_success_cap_duration",
		"stellar_capability_read_contract_error_cap_duration",
		"stellar_capability_write_report_success_cap_duration",
		"stellar_capability_write_report_error_cap_duration",
		"stellar_capability_write_report_duplicate_tx_cap_duration",
		"stellar_capability_write_report_tx_info_retrieval_error_cap_duration",
		"stellar_capability_write_report_successful_early_return_cap_duration",
		"stellar_capability_write_report_invalid_transmission_state_cap_duration",
	}

	require.Len(t, views, len(expectedMetricNames))
	for _, name := range expectedMetricNames {
		stream, ok := metricViewStream(views, name)
		require.True(t, ok, "missing metric view for %s", name)
		aggregation, ok := stream.Aggregation.(sdkmetric.AggregationExplicitBucketHistogram)
		require.True(t, ok, "expected explicit bucket histogram for %s", name)
		require.Equal(t, expectedBuckets, aggregation.Boundaries, "bucket boundaries mismatch for %s", name)
	}
}

func metricViewStream(views []sdkmetric.View, name string) (sdkmetric.Stream, bool) {
	for _, view := range views {
		if stream, ok := view(sdkmetric.Instrument{Name: name}); ok {
			return stream, true
		}
	}
	return sdkmetric.Stream{}, false
}

func TestNewMetrics_RegistrationErrors(t *testing.T) {
	metricNames := []string{
		"read_contract_success",
		"read_contract_error",
		"write_report_success",
		"write_report_error",
		"write_report_duplicate_tx",
		"write_report_tx_info_retrieval_error",
		"write_report_successful_early_return",
		"write_report_invalid_transmission_state",
	}

	orig := newMetricsCapBasic
	defer func() { newMetricsCapBasic = orig }()

	for i, metricName := range metricNames {
		t.Run(metricName, func(t *testing.T) {
			call := 0
			newMetricsCapBasic = func(info capmonitoring.MetricsInfoCapBasic) (capmonitoring.MetricsCapBasic, error) {
				call++
				if call == i+1 {
					return capmonitoring.MetricsCapBasic{}, errors.New("registration failed")
				}
				return orig(info)
			}

			_, err := NewMetrics()
			require.Error(t, err)
			require.Contains(t, err.Error(), metricName)
		})
	}
}
