package monitoring_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
)

func TestNewMetricsCapBasic_WithExplicitBuckets(t *testing.T) {
	buckets := []float64{10, 25, 50, 100, 250, 500}
	reader := useManualMetricReader(t)

	info := capmonitoring.NewMetricsInfoCapBasicWithBuckets("test_metric", "test.event", buckets)
	metrics, err := capmonitoring.NewMetricsCapBasic(info)
	require.NoError(t, err)

	metrics.RecordEmit(t.Context(), 100, 150)

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))

	histogram := findHistogram(resourceMetrics, "test_metric_cap_duration")
	require.NotNil(t, histogram)
	require.Len(t, histogram.DataPoints, 1)
	require.Equal(t, buckets, histogram.DataPoints[0].Bounds)
}

func TestNewMetricsCapBasic_WithoutBuckets(t *testing.T) {
	reader := useManualMetricReader(t)

	info := capmonitoring.NewMetricsInfoCapBasic("test_metric_default", "test.event.default")
	metrics, err := capmonitoring.NewMetricsCapBasic(info)
	require.NoError(t, err)

	metrics.RecordEmit(t.Context(), 100, 200)

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))

	require.NotNil(t, findHistogram(resourceMetrics, "test_metric_default_cap_duration"))
	require.NotNil(t, findCounter(resourceMetrics, "test_metric_default_count"))
}

func useManualMetricReader(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previousClient := beholder.GetClient()
	client := beholder.NewNoopClient()
	client.MeterProvider = provider
	client.Meter = provider.Meter("libs-monitoring-test")
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

func findCounter(resourceMetrics metricdata.ResourceMetrics, name string) *metricdata.Sum[int64] {
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if metric.Name != name {
				continue
			}
			if counter, ok := metric.Data.(metricdata.Sum[int64]); ok {
				return &counter
			}
		}
	}
	return nil
}
