package metrics

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
)

var _ ConsensusMetrics = (*consensusMetrics)(nil)

type consensusMetrics struct {
	chainInfo                   types.ChainInfo
	prefix                      string
	outcomeChainSafeHeight      metric.Int64Gauge
	outcomeChainLatestHeight    metric.Int64Gauge
	outcomeChainFinalizedHeight metric.Int64Gauge
	roundObservationSize        metric.Int64Histogram
	requestObservationSize      metric.Int64Histogram
	identicalResponseCount      metric.Int64Histogram
	queueSize                   metric.Int64Gauge
	retryQueueSize              metric.Int64Gauge
	requestCount                metric.Int64Gauge
}

func newConsensusMetrics(chainInfo types.ChainInfo, metricsNamePrefix string) (*consensusMetrics, error) {
	m := &consensusMetrics{
		chainInfo: chainInfo,
		prefix:    metricsNamePrefix,
	}
	if err := m.init(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *consensusMetrics) init() error {
	meter := beholder.GetMeter()
	var err error

	m.outcomeChainSafeHeight, err = meter.Int64Gauge(
		m.prefix+"capability_consensus_outcome_chain_safe_height",
		metric.WithDescription("reporting plugin for output chain safe height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain safe height gauge: %w", err)
	}

	m.outcomeChainLatestHeight, err = meter.Int64Gauge(
		m.prefix+"capability_consensus_outcome_chain_latest_height",
		metric.WithDescription("reporting plugin for output chain latest height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain latest height gauge: %w", err)
	}

	m.outcomeChainFinalizedHeight, err = meter.Int64Gauge(
		m.prefix+"capability_consensus_outcome_chain_finalized_height",
		metric.WithDescription("reporting plugin for output chain finalized height"),
	)
	if err != nil {
		return fmt.Errorf("failed to create outcome chain finalized height gauge: %w", err)
	}

	m.roundObservationSize, err = meter.Int64Histogram(
		m.prefix+"capability_consensus_round_observation_size",
		metric.WithDescription("Histogram report plugin round observation size in bytes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create round observation size histogram: %w", err)
	}

	m.requestObservationSize, err = meter.Int64Histogram(
		m.prefix+"capability_consensus_request_observation_size",
		metric.WithDescription("Histogram report plugin request observation size in bytes"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request observation size histogram: %w", err)
	}

	m.identicalResponseCount, err = meter.Int64Histogram(
		m.prefix+"capability_consensus_identical_response_count",
		metric.WithDescription("Number of nodes that returned the same response for a capability read request per OCR round"),
	)
	if err != nil {
		return fmt.Errorf("failed to create identical response count histogram: %w", err)
	}

	m.queueSize, err = meter.Int64Gauge(
		m.prefix+"capability_consensus_queue_size",
		metric.WithDescription("Number poller queue size"),
	)
	if err != nil {
		return fmt.Errorf("failed to create queue size gauge: %w", err)
	}

	m.retryQueueSize, err = meter.Int64Gauge(
		m.prefix+"capability_consensus_retry_queue_size",
		metric.WithDescription("Number of poller retry queue size"),
	)
	if err != nil {
		return fmt.Errorf("failed to create retry queue size gauge: %w", err)
	}

	m.requestCount, err = meter.Int64Gauge(
		m.prefix+"capability_consensus_request_count",
		metric.WithDescription("Handler request count"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request count gauge: %w", err)
	}

	return nil
}

func (m *consensusMetrics) chainAttributes() metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("chain_id", m.chainInfo.ChainID),
		attribute.String("family_name", m.chainInfo.FamilyName),
		attribute.String("network_name", m.chainInfo.NetworkName),
		attribute.String("network_name_full", m.chainInfo.NetworkNameFull),
	)
}

func (m *consensusMetrics) RecordOutcomeChainHeight(ctx context.Context, height *ctypes.ChainHeight) {
	if height != nil {
		m.outcomeChainSafeHeight.Record(ctx, height.Safe, m.chainAttributes())
		m.outcomeChainLatestHeight.Record(ctx, height.Latest, m.chainAttributes())
		m.outcomeChainFinalizedHeight.Record(ctx, height.Finalized, m.chainAttributes())
	}
}

func (m *consensusMetrics) RecordRoundObservationSize(ctx context.Context, size int) {
	m.roundObservationSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *consensusMetrics) RecordRequestObservationSize(ctx context.Context, size int) {
	m.requestObservationSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *consensusMetrics) RecordIdenticalResponseCount(ctx context.Context, count int, observationType string) {
	m.identicalResponseCount.Record(ctx, int64(count), metric.WithAttributes(
		attribute.String("chain_id", m.chainInfo.ChainID),
		attribute.String("family_name", m.chainInfo.FamilyName),
		attribute.String("network_name", m.chainInfo.NetworkName),
		attribute.String("network_name_full", m.chainInfo.NetworkNameFull),
		attribute.String("observation_type", observationType),
	))
}

func (m *consensusMetrics) RecordQueueSize(ctx context.Context, size int) {
	m.queueSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *consensusMetrics) RecordRetryQueueSize(ctx context.Context, size int) {
	m.retryQueueSize.Record(ctx, int64(size), m.chainAttributes())
}

func (m *consensusMetrics) SetRequestCount(requestCount int) {
	m.requestCount.Record(context.Background(), int64(requestCount), m.chainAttributes())
}
