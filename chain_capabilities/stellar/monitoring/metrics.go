package monitoring

import (
	"context"
	"fmt"

	commoncapbeholder "github.com/smartcontractkit/capabilities/libs/monitoring"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

func ns(name string) string { return fmt.Sprintf("stellar_capability_%s", name) }

// Metrics holds the per-method instruments for Stellar consensus reads. Each MetricsCapBasic
// records both a count and the request latency (emit - start) as a histogram.
type Metrics struct {
	ReadContractSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	ReadContractError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
}

// NewMetrics constructs the Stellar capability metrics.
func NewMetrics() (Metrics, error) {
	m := Metrics{}
	var err error

	readSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("read_contract_success"), commonbeholder.ToSchemaFullName(&ReadContractSuccess{}))
	m.ReadContractSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(readSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create read contract success metric: %w", err)
	}

	readErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("read_contract_error"), commonbeholder.ToSchemaFullName(&ReadContractError{}))
	m.ReadContractError.basic, err = commoncapbeholder.NewMetricsCapBasic(readErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create read contract error metric: %w", err)
	}

	return m, nil
}

func (m *Metrics) OnReadContractSuccess(ctx context.Context, msg *ReadContractSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.ReadContractSuccess.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnReadContractError(ctx context.Context, msg *ReadContractError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.ReadContractError.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}
