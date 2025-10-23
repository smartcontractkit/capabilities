package metrics

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
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

type evmConsensusMetrics struct {
	chainInfo                   types.ChainInfo
	outcomeChainSafeHeight      metric.Int64Gauge
	outcomeChainLatestHeight    metric.Int64Gauge
	outcomeChainFinalizedHeight metric.Int64Gauge
	roundObservationSize        metric.Int64Histogram
	requestObservationSize      metric.Int64Histogram
	queueSize                   metric.Int64Gauge
	retryQueueSize              metric.Int64Gauge
	requestCount                metric.Int64Gauge
}

func NewEvmConsensusMetrics(chainInfo types.ChainInfo) (*evmConsensusMetrics, error) {
	m := &evmConsensusMetrics{
		chainInfo: chainInfo,
	}
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

	m.outcomeChainSafeHeight, err = meter.Int64Gauge(
		"evm_capability_consensus_outcome_chain_safe_height",
		metric.WithDescription("reporting plugin for output chain safe height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain safe height gauge: %w", err)
	}

	m.outcomeChainLatestHeight, err = meter.Int64Gauge(
		"evm_capability_consensus_outcome_chain_latest_height",
		metric.WithDescription("reporting plugin for output chain latest height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain latest height gauge: %w", err)
	}

	m.outcomeChainFinalizedHeight, err = meter.Int64Gauge(
		"evm_capability_consensus_outcome_chain_finalized_height",
		metric.WithDescription("reporting plugin for output chain finalized height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain finalized height gauge: %w", err)
	}

	m.roundObservationSize, err = meter.Int64Histogram(
		"evm_capability_consensus_round_observation_size",
		metric.WithDescription("Histogram report plugin round observation size in bytes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create round observation size histogram: %w", err)
	}

	m.requestObservationSize, err = meter.Int64Histogram(
		"evm_capability_consensus_request_observation_size",
		metric.WithDescription("Histogram report plugin request observation size in bytes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request observation size histogram: %w", err)
	}

	m.queueSize, err = meter.Int64Gauge(
		"evm_capability_consensus_queue_size",
		metric.WithDescription("Number poller queue size"),
	)
	if err != nil {
		return fmt.Errorf("failed to create queue size gauge: %w", err)
	}

	m.retryQueueSize, err = meter.Int64Gauge(
		"evm_capability_consensus_retry_queue_size",
		metric.WithDescription("Number of poller retry queue size"),
	)
	if err != nil {
		return fmt.Errorf("failed to create retry queue size gauge: %w", err)
	}

	m.requestCount, err = meter.Int64Gauge(
		"evm_capability_consensus_request_count",
		metric.WithDescription("Handler request count"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request count gauge: %w", err)
	}

	return nil
}

func (m *evmConsensusMetrics) chainAttributes() metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("chain_id", m.chainInfo.ChainID),
		attribute.String("family_name", m.chainInfo.FamilyName),
		attribute.String("network_name", m.chainInfo.NetworkName),
		attribute.String("network_name_full", m.chainInfo.NetworkNameFull),
	)
}

func (m *evmConsensusMetrics) RecordOutcomeChainHeight(ctx context.Context, height *ctypes.ChainHeight) {
	if height != nil {
		m.outcomeChainSafeHeight.Record(ctx, height.Safe, m.chainAttributes())
		m.outcomeChainLatestHeight.Record(ctx, height.Latest, m.chainAttributes())
		m.outcomeChainFinalizedHeight.Record(ctx, height.Finalized, m.chainAttributes())
	}
}

func (m *evmConsensusMetrics) RecordRoundObservationSize(ctx context.Context, size int) {
	m.roundObservationSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *evmConsensusMetrics) RecordRequestObservationSize(ctx context.Context, size int) {
	m.requestObservationSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *evmConsensusMetrics) RecordQueueSize(ctx context.Context, size int) {
	m.queueSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *evmConsensusMetrics) RecordRetryQueueSize(ctx context.Context, size int) {
	m.retryQueueSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *evmConsensusMetrics) SetRequestCount(requestCount int) {
	m.requestCount.Record(context.Background(), int64(requestCount), m.chainAttributes())
}
