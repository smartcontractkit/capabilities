package trigger

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

// Metrics contains metrics for cron capability
type Metrics struct {
	activeTriggersGauge          metric.Int64Gauge
	triggeredCount               metric.Int64Counter
	triggerDistributionHistogram metric.Float64Histogram

	activeTriggers int64
	mux            sync.Mutex
}

func MetricViews() []sdkmetric.View {
	return []sdkmetric.View{
		sdkmetric.NewView(
			sdkmetric.Instrument{Name: "cron_capability_trigger_distribution"},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 59},
			}},
		),
	}
}

// NewMetrics creates a new instance of Metrics
func NewMetrics() (*Metrics, error) {
	m := &Metrics{}
	if err := m.init(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Metrics) init() error {
	meter := beholder.GetMeter()
	var err error

	m.activeTriggersGauge, err = meter.Int64Gauge(
		"cron_capability_active_triggers_gauge",
		metric.WithDescription("Number of active cron triggers"),
	)
	if err != nil {
		return fmt.Errorf("failed to create active triggers gauge: %w", err)
	}

	m.triggeredCount, err = meter.Int64Counter(
		"cron_capability_triggered_count",
		metric.WithDescription("The number of times the cron trigger capability has been triggered"),
	)
	if err != nil {
		return fmt.Errorf("failed to create triggered count counter: %w", err)
	}

	m.triggerDistributionHistogram, err = meter.Float64Histogram(
		"cron_capability_trigger_distribution",
		metric.WithDescription("Distribution of trigger execution times over 5 second intervals"),
	)
	if err != nil {
		return fmt.Errorf("failed to create trigger distribution histogram: %w", err)
	}

	return nil
}

func (m *Metrics) IncActiveTriggersGauge(ctx context.Context) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.activeTriggers++
	m.activeTriggersGauge.Record(ctx, m.activeTriggers)
}

func (m *Metrics) DecActiveTriggersGauge(ctx context.Context) {
	m.mux.Lock()
	defer m.mux.Unlock()
	m.activeTriggers--
	m.activeTriggersGauge.Record(ctx, m.activeTriggers)
}

func (m *Metrics) IncTriggeredCount(ctx context.Context, status string) {
	m.triggeredCount.Add(ctx, 1, metric.WithAttributes(attribute.String("status", status)))
}

func (m *Metrics) RecordTriggerExecutionTime(ctx context.Context) {
	now := time.Now()
	// Get the current second within the minute (0-59)
	seconds := now.Second()
	m.triggerDistributionHistogram.Record(ctx, float64(seconds))
}
