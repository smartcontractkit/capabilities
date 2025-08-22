package common

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
)

// Metrics contains metrics for HTTP actions
type Metrics struct {
	requestCount            metric.Int64Counter
	inputValidationFailures metric.Int64Counter
	workflowOwnerThrottled  metric.Int64Counter
	nodeThrottled           metric.Int64Counter
	gatewayConnectionError  metric.Int64Counter
	gatewaySendError        metric.Int64Counter
	successfulResponse      metric.Int64Counter
	executionError          metric.Int64Counter
	gatewayNodeThrottled    metric.Int64Counter
	gatewayGlobalThrottled  metric.Int64Counter
	requestLatency          metric.Int64Histogram
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

	m.requestCount, err = meter.Int64Counter(
		"http_action_request_count",
		metric.WithDescription("Number of HTTP action requests"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request count metric: %w", err)
	}

	m.inputValidationFailures, err = meter.Int64Counter(
		"http_action_validation_failure_count",
		metric.WithDescription("Number of HTTP action input validation failures"),
	)
	if err != nil {
		return fmt.Errorf("failed to create validation failure metric: %w", err)
	}

	m.workflowOwnerThrottled, err = meter.Int64Counter(
		"http_action_workflow_owner_throttled_count",
		metric.WithDescription("Number of HTTP action requests exceeding per-workflow-owner rate limit"),
	)
	if err != nil {
		return fmt.Errorf("failed to create workflow owner throttled metric: %w", err)
	}

	m.nodeThrottled, err = meter.Int64Counter(
		"http_action_node_throttled_count",
		metric.WithDescription("Number of HTTP action requests exceeding global rate limit"),
	)
	if err != nil {
		return fmt.Errorf("failed to create node throttled metric: %w", err)
	}

	m.gatewayConnectionError, err = meter.Int64Counter(
		"http_action_capability_gateway_connection_error_count",
		metric.WithDescription("Number of HTTP action gateway connection errors"),
	)
	if err != nil {
		return fmt.Errorf("failed to create capability gateway connection error metric: %w", err)
	}

	m.gatewaySendError, err = meter.Int64Counter(
		"http_action_capability_gateway_send_error_count",
		metric.WithDescription("Number of HTTP action gateway send errors"),
	)
	if err != nil {
		return fmt.Errorf("failed to create capability gateway send error metric: %w", err)
	}

	m.successfulResponse, err = meter.Int64Counter(
		"http_action_successful_response_count",
		metric.WithDescription("Number of HTTP action successful responses"),
	)
	if err != nil {
		return fmt.Errorf("failed to create successful response metric: %w", err)
	}

	m.executionError, err = meter.Int64Counter(
		"http_action_execution_error_count",
		metric.WithDescription("Number of HTTP action execution errors"),
	)
	if err != nil {
		return fmt.Errorf("failed to create execution error metric: %w", err)
	}

	m.gatewayNodeThrottled, err = meter.Int64Counter(
		"http_action_capability_gateway_node_throttled_count",
		metric.WithDescription("Number of throttled requests while receiving HTTP action response from gateway. Per-gateway-node rate limit"),
	)
	if err != nil {
		return fmt.Errorf("failed to create capability gateway node throttled metric: %w", err)
	}

	m.gatewayGlobalThrottled, err = meter.Int64Counter(
		"http_action_capability_gateway_global_throttled_count",
		metric.WithDescription("Number of throttled requests while receiving HTTP action response from gateway. Global limit"),
	)
	if err != nil {
		return fmt.Errorf("failed to create capability gateway global throttled metric: %w", err)
	}

	m.requestLatency, err = meter.Int64Histogram(
		"http_action_request_latency_ms",
		metric.WithDescription("HTTP action request latency in milliseconds"),
	)
	if err != nil {
		return fmt.Errorf("failed to create request latency metric: %w", err)
	}

	return nil
}

func (m *Metrics) IncrementRequestCount(ctx context.Context, lggr logger.Logger) {
	m.requestCount.Add(ctx, 1)
}

func (m *Metrics) IncrementInputValidationFailures(ctx context.Context, lggr logger.Logger) {
	m.inputValidationFailures.Add(ctx, 1)
}

func (m *Metrics) IncrementWorkflowOwnerThrottled(ctx context.Context, lggr logger.Logger) {
	m.workflowOwnerThrottled.Add(ctx, 1)
}

func (m *Metrics) IncrementNodeThrottled(ctx context.Context, lggr logger.Logger) {
	m.nodeThrottled.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayConnectionError(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	m.gatewayConnectionError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementGatewaySendError(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	m.gatewaySendError.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementSuccessfulResponse(ctx context.Context, lggr logger.Logger) {
	m.successfulResponse.Add(ctx, 1)
}

func (m *Metrics) IncrementExecutionError(ctx context.Context, lggr logger.Logger) {
	m.executionError.Add(ctx, 1)
}

func (m *Metrics) IncrementGatewayNodeThrottled(ctx context.Context, nodeAddress string, lggr logger.Logger) {
	m.gatewayNodeThrottled.Add(ctx, 1, metric.WithAttributes(attribute.String(AttrNodeAddress, nodeAddress)))
}

func (m *Metrics) IncrementGatewayGlobalThrottled(ctx context.Context, lggr logger.Logger) {
	m.gatewayGlobalThrottled.Add(ctx, 1)
}

func (m *Metrics) RecordRequestLatency(ctx context.Context, latencyMs int64, lggr logger.Logger) {
	m.requestLatency.Record(ctx, latencyMs)
}
