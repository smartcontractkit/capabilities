package metrics

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

type EvmConsensusMetrics interface {
	// metrics for consensus' reporting_plugin
	RecordOutcomeChainHeight(ctx context.Context, height *ctypes.ChainHeight)
	RecordRoundObservationSize(ctx context.Context, size int)
	RecordRequestObservationSize(ctx context.Context, size int)

	// metrics for consensus' poller
	RecordQueueSize(ctx context.Context, size int)
	RecordRetryQueueSize(ctx context.Context, size int)

	// metrics for consensus' handler
	SetRequestCount(requestCount int)
}

var _ EvmConsensusMetrics = (*evmConsensusMetrics)(nil)

// evmConsensusMetrics contains evmConsensusMetrics for consensus capability
type evmConsensusMetrics struct {
	OutcomeChainSafeHeightGauge      metric.Int64Gauge
	OutcomeChainLatestHeightGauge    metric.Int64Gauge
	OutcomeChainFinalizedHeightGauge metric.Int64Gauge
	RoundObservationSizeHistogram    metric.Int64Histogram
	RequestObservationSizeHistogram  metric.Int64Histogram
	QueueSizeGauge                   metric.Int64Gauge
	RetryQueueSizeGauge              metric.Int64Gauge
	RequestCountGauge                metric.Int64Gauge
}

// NewEvmConsensusMetrics creates a new instance of evmConsensusMetrics
func NewEvmConsensusMetrics() (*evmConsensusMetrics, error) {
	m := &evmConsensusMetrics{}
	if err := m.init(); err != nil {
		return nil, err
	}
	return m, nil
}

func MetricViews() []sdkmetric.View {
	instrumentNames := []string{
		"evm_capability_consensus_round_observation_size",
		"evm_capability_consensus_request_observation_size",
	}
	var views []sdkmetric.View
	for _, name := range instrumentNames {
		views = append(views, sdkmetric.NewView(
			sdkmetric.Instrument{Name: name},
			sdkmetric.Stream{Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
				Boundaries: []float64{0, 10, 100, 1000, 10000, 100000, 1000000},
			}},
		))
	}
	return views
}

func (m *evmConsensusMetrics) init() error {
	meter := beholder.GetMeter()
	var err error

	m.OutcomeChainSafeHeightGauge, err = meter.Int64Gauge(
		"evm_capability_consensus_outcome_chain_safe_height",
		metric.WithDescription("reporting plugin for output chain safe height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain safe height gauge: %w", err)
	}

	m.OutcomeChainLatestHeightGauge, err = meter.Int64Gauge(
		"evm_capability_consensus_outcome_chain_latest_height",
		metric.WithDescription("reporting plugin for output chain latest height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain latest height gauge: %w", err)
	}

	m.OutcomeChainFinalizedHeightGauge, err = meter.Int64Gauge(
		"evm_capability_consensus_outcome_chain_finalized_height",
		metric.WithDescription("reporting plugin for output chain finalized height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain finalized height gauge: %w", err)
	}

	m.RoundObservationSizeHistogram, err = meter.Int64Histogram(
		"evm_capability_consensus_round_observation_size",
		metric.WithDescription("Histogram report plugin round observation size in bytes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create round observation size histogram: %w", err)
	}

	m.RequestObservationSizeHistogram, err = meter.Int64Histogram(
		"evm_capability_consensus_request_observation_size",
		metric.WithDescription("Histogram report plugin request observation size in bytes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request observation size histogram: %w", err)
	}

	m.QueueSizeGauge, err = meter.Int64Gauge(
		"evm_capability_consensus_queue_size",
		metric.WithDescription("Number poller queue size"),
	)
	if err != nil {
		return fmt.Errorf("failed to create queue size gauge: %w", err)
	}

	m.RetryQueueSizeGauge, err = meter.Int64Gauge(
		"evm_capability_consensus_retry_queue_size",
		metric.WithDescription("Number of poller retry queue size"),
	)
	if err != nil {
		return fmt.Errorf("failed to create retry queue size gauge: %w", err)
	}

	m.RequestCountGauge, err = meter.Int64Gauge(
		"evm_capability_consensus_request_count",
		metric.WithDescription("Handler request count"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request count gauge: %w", err)
	}

	return nil
}

func (m *evmConsensusMetrics) RecordOutcomeChainHeight(ctx context.Context, height *ctypes.ChainHeight) {
	if height != nil {
		m.OutcomeChainSafeHeightGauge.Record(ctx, height.Safe)
		m.OutcomeChainLatestHeightGauge.Record(ctx, height.Latest)
		m.OutcomeChainFinalizedHeightGauge.Record(ctx, height.Finalized)
	}
}

func (m *evmConsensusMetrics) RecordRoundObservationSize(ctx context.Context, size int) {
	m.RoundObservationSizeHistogram.Record(ctx, int64(size))
}

func (m *evmConsensusMetrics) RecordRequestObservationSize(ctx context.Context, size int) {
	m.RequestObservationSizeHistogram.Record(ctx, int64(size))
}

func (m *evmConsensusMetrics) RecordQueueSize(ctx context.Context, size int) {
	m.QueueSizeGauge.Record(ctx, int64(size))
}

func (m *evmConsensusMetrics) RecordRetryQueueSize(ctx context.Context, size int) {
	m.RetryQueueSizeGauge.Record(ctx, int64(size))
}

func (m *evmConsensusMetrics) SetRequestCount(requestCount int) {
	m.RequestCountGauge.Record(context.Background(), int64(requestCount))
}
