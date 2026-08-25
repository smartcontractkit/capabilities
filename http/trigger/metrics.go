package trigger

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	AttrNodeAddress = "node_address"
	AttrMethodName  = "method_name"
)

// Metrics contains metrics for HTTP triggers
type Metrics struct {
	registerCount             metric.Int64Counter
	deregisterCount           metric.Int64Counter
	registerFailureCount      metric.Int64Counter
	deregisterFailureCount    metric.Int64Counter
	requestCacheCleanUpCount  metric.Int64Counter
	requestCount              metric.Int64Counter
	gatewayGlobalThrottled    metric.Int64Counter
	gatewayNodeThrottled      metric.Int64Counter
	gatewayRequestCount       metric.Int64Counter
	gatewaySendError          metric.Int64Counter
	broadcastMetadataCount    metric.Int64Counter
	broadcastMetadataFailures metric.Int64Counter
	broadcastMetadataLatency  metric.Int64Histogram
	pullMetadataCount         metric.Int64Counter
	pullMetadataFailures      metric.Int64Counter
	pullMetadataLatency       metric.Int64Histogram
	requestLatency            metric.Int64Histogram
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

	m.registerCount, err = meter.Int64Counter(
		"http_trigger_register_count",
		metric.WithDescription("Number of HTTP trigger registrations"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger register count metric: %w", err)
	}

	m.deregisterCount, err = meter.Int64Counter(
		"http_trigger_deregister_count",
		metric.WithDescription("Number of HTTP trigger deregistrations"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger deregister count metric: %w", err)
	}

	m.registerFailureCount, err = meter.Int64Counter(
		"http_trigger_register_failure_count",
		metric.WithDescription("Number of HTTP trigger registration failures"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger register failure count metric: %w", err)
	}

	m.deregisterFailureCount, err = meter.Int64Counter(
		"http_trigger_deregister_failure_count",
		metric.WithDescription("Number of HTTP trigger deregistration failures"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger deregister failure count metric: %w", err)
	}

	m.requestCacheCleanUpCount, err = meter.Int64Counter(
		"http_trigger_request_cache_cleanup_count",
		metric.WithDescription("Number of expired entries cleaned up from HTTP trigger request cache"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger request cache cleanup count metric: %w", err)
	}

	m.requestCount, err = meter.Int64Counter(
		"http_trigger_capability_request_count",
		metric.WithDescription("Number of HTTP trigger requests processed"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability request count metric: %w", err)
	}

	m.gatewayGlobalThrottled, err = meter.Int64Counter(
		"http_trigger_capability_gateway_global_throttled",
		metric.WithDescription("Number of HTTP trigger requests throttled due to global rate limit"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability gateway global throttled metric: %w", err)
	}

	m.gatewayNodeThrottled, err = meter.Int64Counter(
		"http_trigger_capability_gateway_node_throttled",
		metric.WithDescription("Number of HTTP trigger requests throttled due to per-node rate limit"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability gateway node throttled metric: %w", err)
	}

	m.gatewayRequestCount, err = meter.Int64Counter(
		"http_trigger_capability_gateway_request_count",
		metric.WithDescription("Number of HTTP trigger requests sent to gateway"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability gateway request count metric: %w", err)
	}

	m.gatewaySendError, err = meter.Int64Counter(
		"http_trigger_capability_gateway_send_error",
		metric.WithDescription("Number of HTTP trigger gateway send errors"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability gateway send error metric: %w", err)
	}

	m.broadcastMetadataCount, err = meter.Int64Counter(
		"http_trigger_capability_broadcast_metadata_count",
		metric.WithDescription("Number of HTTP trigger broadcast metadata workflow operations"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability broadcast metadata count metric: %w", err)
	}

	m.broadcastMetadataFailures, err = meter.Int64Counter(
		"http_trigger_capability_broadcast_metadata_failures",
		metric.WithDescription("Number of HTTP trigger broadcast metadata workflow failures"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability broadcast metadata failures metric: %w", err)
	}

	m.broadcastMetadataLatency, err = meter.Int64Histogram(
		"http_trigger_capability_broadcast_metadata_latency_ms",
		metric.WithDescription("HTTP trigger broadcast metadata latency in milliseconds"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability broadcast metadata latency metric: %w", err)
	}

	m.pullMetadataCount, err = meter.Int64Counter(
		"http_trigger_capability_pull_metadata_count",
		metric.WithDescription("Number of HTTP trigger pull metadata workflow operations"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability pull metadata count metric: %w", err)
	}

	m.pullMetadataFailures, err = meter.Int64Counter(
		"http_trigger_capability_pull_metadata_failures",
		metric.WithDescription("Number of HTTP trigger pull metadata workflow failures"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability pull metadata failures metric: %w", err)
	}

	m.pullMetadataLatency, err = meter.Int64Histogram(
		"http_trigger_capability_pull_metadata_latency_ms",
		metric.WithDescription("HTTP trigger pull metadata latency in milliseconds"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability pull metadata latency metric: %w", err)
	}

	m.requestLatency, err = meter.Int64Histogram(
		"http_trigger_capability_request_latency_ms",
		metric.WithDescription("HTTP trigger capability request processing latency in milliseconds"),
	)
	if err != nil {
		return fmt.Errorf("failed to create HTTP trigger capability request latency metric: %w", err)
	}

	return nil
}

func (m *Metrics) IncrementRegisterCount(ctx context.Context, lggr logger.Logger) {
	m.registerCount.Add(ctx, 1)
}

func (m *Metrics) IncrementDeregisterCount(ctx context.Context, lggr logger.Logger) {
	m.deregisterCount.Add(ctx, 1)
}

func (m *Metrics) IncrementRegisterFailureCount(ctx context.Context, lggr logger.Logger) {
	m.registerFailureCount.Add(ctx, 1)
}

func (m *Metrics) IncrementDeregisterFailureCount(ctx context.Context, lggr logger.Logger) {
	m.deregisterFailureCount.Add(ctx, 1)
}

func (m *Metrics) IncrementRequestCacheCleanUpCount(ctx context.Context, count int64, lggr logger.Logger) {
	m.requestCacheCleanUpCount.Add(ctx, count)
}

func (m *Metrics) IncrementRequestCount(ctx context.Context, lggr logger.Logger) {
	m.requestCount.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayGlobalThrottled(ctx context.Context, lggr logger.Logger) {
	m.gatewayGlobalThrottled.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayNodeThrottled(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	m.gatewayNodeThrottled.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementGatewayRequestCount(ctx context.Context, nodeAddress string, methodName string, lggr logger.Logger) {
	m.gatewayRequestCount.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress),
		attribute.String(AttrMethodName, methodName)))
}

func (m *Metrics) IncrementGatewaySendError(ctx context.Context, nodeAddress string, methodName string, lggr logger.Logger) {
	m.gatewaySendError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress),
		attribute.String(AttrMethodName, methodName)))
}

func (m *Metrics) IncrementBroadcastMetadataCount(ctx context.Context, lggr logger.Logger) {
	m.broadcastMetadataCount.Add(ctx, 1)
}

func (m *Metrics) IncrementBroadcastMetadataFailures(ctx context.Context, lggr logger.Logger) {
	m.broadcastMetadataFailures.Add(ctx, 1)
}

func (m *Metrics) RecordBroadcastMetadataLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	m.broadcastMetadataLatency.Record(ctx, latencyMs)
}

func (m *Metrics) IncrementPullMetadataCount(ctx context.Context, lggr logger.Logger) {
	m.pullMetadataCount.Add(ctx, 1)
}

func (m *Metrics) IncrementPullMetadataFailures(ctx context.Context, lggr logger.Logger) {
	m.pullMetadataFailures.Add(ctx, 1)
}

func (m *Metrics) RecordPullMetadataLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	m.pullMetadataLatency.Record(ctx, latencyMs)
}

func (m *Metrics) RecordRequestLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	m.requestLatency.Record(ctx, latencyMs)
}
