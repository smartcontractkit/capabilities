package monitoring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

func setupTestMeter(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	meter := provider.Meter("test")

	prev := beholder.GetClient()
	beholder.SetClient(&beholder.Client{Meter: meter})
	t.Cleanup(func() { beholder.SetClient(prev) })

	return reader
}

func TestNewMetricsCapBasic_DefaultBuckets(t *testing.T) {
	reader := setupTestMeter(t)

	info := NewMetricsInfoCapBasic("test_metric", "test_event")
	m, err := NewMetricsCapBasic(info)
	require.NoError(t, err)

	m.RecordEmit(context.Background(), 0, 5000)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	hist := findHistogram(t, rm, "test_metric_cap_duration")
	require.NotNil(t, hist)

	// Without explicit buckets, OTel SDK uses its own defaults (not empty).
	assert.NotEmpty(t, hist.Bounds)
	// Default OTel SDK boundaries cap at 10_000.
	assert.Equal(t, float64(10_000), hist.Bounds[len(hist.Bounds)-1])
}

func TestNewMetricsCapBasic_WithHistogramBuckets(t *testing.T) {
	reader := setupTestMeter(t)

	customBuckets := []float64{0, 100, 500, 1000, 5000, 10000, 30000, 60000}

	info := NewMetricsInfoCapBasic("test_custom", "test_event")
	m, err := NewMetricsCapBasic(info, WithHistogramBuckets(customBuckets...))
	require.NoError(t, err)

	m.RecordEmit(context.Background(), 0, 15000)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))

	hist := findHistogram(t, rm, "test_custom_cap_duration")
	require.NotNil(t, hist)

	assert.Equal(t, customBuckets, hist.Bounds)
}

func findHistogram(t *testing.T, rm metricdata.ResourceMetrics, name string) *metricdata.HistogramDataPoint[int64] {
	t.Helper()
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			h, ok := m.Data.(metricdata.Histogram[int64])
			if !ok {
				t.Fatalf("metric %s is not a histogram", name)
			}
			if len(h.DataPoints) == 0 {
				t.Fatalf("metric %s has no data points", name)
			}
			return &h.DataPoints[0]
		}
	}
	return nil
}
