package monitoring

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

const defaultPluginHeartbeatCadence = 5 * time.Minute

// DefaultPluginHeartbeatCadence matches the workflow engine heartbeat cadence.
func DefaultPluginHeartbeatCadence() time.Duration {
	return defaultPluginHeartbeatCadence
}

// PluginHeartbeat exposes liveness gauges for the EVM capability plugin process.
type PluginHeartbeat struct {
	gauge   metric.Int64Gauge
	counter metric.Int64Counter
}

func NewPluginHeartbeat() (*PluginHeartbeat, error) {
	meter := commonbeholder.GetMeter()

	gauge, err := meter.Int64Gauge(ns("evm_capability_heartbeat"),
		metric.WithDescription("1 while the EVM capability plugin is running, 0 after shutdown"))
	if err != nil {
		return nil, fmt.Errorf("failed to register evm capability heartbeat gauge: %w", err)
	}

	counter, err := meter.Int64Counter(ns("evm_capability_heartbeat_total"),
		metric.WithDescription("Periodic EVM capability plugin liveness pulses while running"))
	if err != nil {
		return nil, fmt.Errorf("failed to register evm capability heartbeat counter: %w", err)
	}

	return &PluginHeartbeat{gauge: gauge, counter: counter}, nil
}

func (h *PluginHeartbeat) attrs(chainID uint64) metric.MeasurementOption {
	return metric.WithAttributes(
		attribute.String("chain_id", fmt.Sprintf("%d", chainID)),
	)
}

func (h *PluginHeartbeat) SetAlive(ctx context.Context, chainID uint64, alive bool) {
	val := int64(0)
	if alive {
		val = 1
	}
	h.gauge.Record(ctx, val, h.attrs(chainID))
}

func (h *PluginHeartbeat) Pulse(ctx context.Context, chainID uint64) {
	h.counter.Add(ctx, 1, h.attrs(chainID))
}
