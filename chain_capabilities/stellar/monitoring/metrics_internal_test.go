package monitoring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"

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

func TestNewMetrics_CapDurationBuckets(t *testing.T) {
	reader := useManualMetricReader(t)

	metrics, err := NewMetrics()
	require.NoError(t, err)

	ec := &capmonitoring.ExecutionContext{
		MetaCapabilityTimestampStart: 100,
		MetaCapabilityTimestampEmit:  150,
	}
	require.NoError(t, metrics.OnWriteReportSuccess(t.Context(), &WriteReportSuccess{ExecutionContext: ec}))

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))

	histogram := findHistogram(resourceMetrics, "stellar_capability_write_report_success_cap_duration")
	require.NotNil(t, histogram)
	require.Len(t, histogram.DataPoints, 1)
	require.Equal(t, capDurationBucketBoundariesMs, histogram.DataPoints[0].Bounds)
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

func useManualMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previousClient := beholder.GetClient()
	client := beholder.NewNoopClient()
	client.MeterProvider = provider
	client.Meter = provider.Meter("stellar-monitoring-test")
	beholder.SetClient(client)

	t.Cleanup(func() {
		beholder.SetClient(previousClient)
		require.NoError(t, provider.Shutdown(context.Background()))
	})

	return reader
}

func findHistogram(resourceMetrics metricdata.ResourceMetrics, name string) *metricdata.Histogram[int64] {
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if metric.Name != name {
				continue
			}
			if histogram, ok := metric.Data.(metricdata.Histogram[int64]); ok {
				return &histogram
			}
		}
	}
	return nil
}
