package monitoring

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

type MetricsInfoCapBasic struct {
	count             beholder.MetricInfo
	capTimestampStart beholder.MetricInfo
	capTimestampEmit  beholder.MetricInfo
	capDuration       beholder.MetricInfo // ts.emit - ts.start
}

// NewMetricsInfoCapBasic creates a new MetricsInfoCapBasic using the provided event/metric information
func NewMetricsInfoCapBasic(metricPrefix, eventRef string) MetricsInfoCapBasic {
	return MetricsInfoCapBasic{
		count: beholder.MetricInfo{
			Name:        fmt.Sprintf("%s_count", metricPrefix),
			Unit:        "",
			Description: fmt.Sprintf("The count of message: '%s' emitted", eventRef),
		},
		capTimestampStart: beholder.MetricInfo{
			Name:        fmt.Sprintf("%s_cap_timestamp_start", metricPrefix),
			Unit:        "ms",
			Description: fmt.Sprintf("The timestamp (local) at capability exec start that resulted in message: '%s' emit", eventRef),
		},
		capTimestampEmit: beholder.MetricInfo{
			Name:        fmt.Sprintf("%s_cap_timestamp_emit", metricPrefix),
			Unit:        "ms",
			Description: fmt.Sprintf("The timestamp (local) at message: '%s' emit", eventRef),
		},
		capDuration: beholder.MetricInfo{
			Name:        fmt.Sprintf("%s_cap_duration", metricPrefix),
			Unit:        "ms",
			Description: fmt.Sprintf("The duration (local) since capability exec start to message: '%s' emit", eventRef),
		},
	}
}

// MetricsCapBasic is a base struct for metrics related to a capability
type MetricsCapBasic struct {
	count             metric.Int64Counter
	capTimestampStart metric.Int64Gauge
	capTimestampEmit  metric.Int64Gauge
	capDuration       metric.Int64Histogram // ts.emit - ts.start
}

// CapBasicOption configures optional behaviour for NewMetricsCapBasic.
type CapBasicOption func(*capBasicConfig)

type capBasicConfig struct {
	histogramBuckets []float64
}

// WithHistogramBuckets overrides the default OTel SDK histogram bucket
// boundaries for the capDuration histogram.
func WithHistogramBuckets(boundaries ...float64) CapBasicOption {
	return func(c *capBasicConfig) {
		c.histogramBuckets = boundaries
	}
}

// NewMetricsCapBasic creates a new MetricsCapBasic using the provided MetricsInfoCapBasic.
// Pass WithHistogramBuckets to override the default histogram bucket boundaries.
func NewMetricsCapBasic(info MetricsInfoCapBasic, opts ...CapBasicOption) (MetricsCapBasic, error) {
	var cfg capBasicConfig
	for _, o := range opts {
		o(&cfg)
	}

	meter := beholder.GetMeter()
	set := MetricsCapBasic{}

	var err error

	set.count, err = info.count.NewInt64Counter(meter)
	if err != nil {
		return set, fmt.Errorf("failed to create new counter: %w", err)
	}

	set.capTimestampStart, err = info.capTimestampStart.NewInt64Gauge(meter)
	if err != nil {
		return set, fmt.Errorf("failed to create new gauge: %w", err)
	}

	set.capTimestampEmit, err = info.capTimestampEmit.NewInt64Gauge(meter)
	if err != nil {
		return set, fmt.Errorf("failed to create new gauge: %w", err)
	}

	if len(cfg.histogramBuckets) > 0 {
		set.capDuration, err = meter.Int64Histogram(
			info.capDuration.Name,
			metric.WithUnit(info.capDuration.Unit),
			metric.WithDescription(info.capDuration.Description),
			metric.WithExplicitBucketBoundaries(cfg.histogramBuckets...),
		)
	} else {
		set.capDuration, err = info.capDuration.NewInt64Histogram(meter)
	}
	if err != nil {
		return set, fmt.Errorf("failed to create new histogram: %w", err)
	}

	return set, nil
}

func (m *MetricsCapBasic) RecordEmit(ctx context.Context, start, emit uint64, attrKVs ...attribute.KeyValue) {
	// Define attributes
	attrs := metric.WithAttributes(attrKVs...)

	// Count events
	m.count.Add(ctx, 1, attrs)

	// Timestamp events
	m.capTimestampStart.Record(ctx, int64(start), attrs)
	m.capTimestampEmit.Record(ctx, int64(emit), attrs)
	m.capDuration.Record(ctx, int64(emit-start), attrs)
}

func RequestID(workflowExecutionID, reference string) string {
	return workflowExecutionID + ":" + reference
}
