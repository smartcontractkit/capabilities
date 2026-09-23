package monitoring_test

import (
	"context"
	"math"
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

func TestMetricsCapBasic_RecordEmitSkipsReversedTimestamps(t *testing.T) {
	reader := useManualMetricReader(t)

	info := capmonitoring.NewMetricsInfoCapBasic("test_metric_reversed", "test.event.reversed")
	metrics, err := capmonitoring.NewMetricsCapBasic(info)
	require.NoError(t, err)

	metrics.RecordEmit(t.Context(), 200, 100)

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))

	require.EqualValues(t, 1, counterValue(t, resourceMetrics, "test_metric_reversed_count"))
	require.EqualValues(t, 1, counterValue(t, resourceMetrics, "test_metric_reversed_invalid_telemetry_count"))
	require.Nil(t, findHistogram(resourceMetrics, "test_metric_reversed_cap_duration"))
	require.Nil(t, findGauge(resourceMetrics, "test_metric_reversed_cap_timestamp_start"))
	require.Nil(t, findGauge(resourceMetrics, "test_metric_reversed_cap_timestamp_emit"))
}

func TestMetricsCapBasic_RecordEmitSkipsTimestampsThatOverflowInt64(t *testing.T) {
	reader := useManualMetricReader(t)

	info := capmonitoring.NewMetricsInfoCapBasic("test_metric_overflow", "test.event.overflow")
	metrics, err := capmonitoring.NewMetricsCapBasic(info)
	require.NoError(t, err)

	metrics.RecordEmit(t.Context(), uint64(math.MaxInt64)+1, uint64(math.MaxInt64)+2)

	var resourceMetrics metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &resourceMetrics))

	require.EqualValues(t, 1, counterValue(t, resourceMetrics, "test_metric_overflow_count"))
	require.EqualValues(t, 1, counterValue(t, resourceMetrics, "test_metric_overflow_invalid_telemetry_count"))
	require.Nil(t, findHistogram(resourceMetrics, "test_metric_overflow_cap_duration"))
	require.Nil(t, findGauge(resourceMetrics, "test_metric_overflow_cap_timestamp_start"))
	require.Nil(t, findGauge(resourceMetrics, "test_metric_overflow_cap_timestamp_emit"))
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

func findGauge(resourceMetrics metricdata.ResourceMetrics, name string) *metricdata.Gauge[int64] {
	for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
		for _, metric := range scopeMetrics.Metrics {
			if metric.Name != name {
				continue
			}
			if gauge, ok := metric.Data.(metricdata.Gauge[int64]); ok {
				return &gauge
			}
		}
	}
	return nil
}

func counterValue(t *testing.T, resourceMetrics metricdata.ResourceMetrics, name string) int64 {
	t.Helper()

	counter := findCounter(resourceMetrics, name)
	require.NotNil(t, counter)
	require.Len(t, counter.DataPoints, 1)
	return counter.DataPoints[0].Value
}
