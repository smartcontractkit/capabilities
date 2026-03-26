package monitoring

import (
	"context"
	"fmt"

	commoncapbeholder "github.com/smartcontractkit/capabilities/libs/monitoring"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

func ns(name string) string { return fmt.Sprintf("apt_capability_%s", name) }

// Metrics holds all per-method instruments
type Metrics struct {
	WriteReportSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportDuplicateTx struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportTxFeeCalculationError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
}

// NewMetrics constructs all counters & histograms
func NewMetrics() (Metrics, error) {
	m := Metrics{}
	var err error

	wrSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_success"), commonbeholder.ToSchemaFullName(&WriteReportSuccess{}))
	m.WriteReportSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(wrSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report success metric: %w", err)
	}

	wrErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_error"), commonbeholder.ToSchemaFullName(&WriteReportError{}))
	m.WriteReportError.basic, err = commoncapbeholder.NewMetricsCapBasic(wrErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report error metric: %w", err)
	}

	wrDupTx := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_duplicate_tx"), commonbeholder.ToSchemaFullName(&WriteReportDuplicateTx{}))
	m.WriteReportDuplicateTx.basic, err = commoncapbeholder.NewMetricsCapBasic(wrDupTx)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report duplicate tx metric: %w", err)
	}

	wrFeeErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_tx_fee_calculation_error"), commonbeholder.ToSchemaFullName(&WriteReportTxFeeCalculationError{}))
	m.WriteReportTxFeeCalculationError.basic, err = commoncapbeholder.NewMetricsCapBasic(wrFeeErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report tx fee calculation error metric: %w", err)
	}

	return m, nil
}

func (m *Metrics) OnWriteReportSuccess(ctx context.Context, msg *WriteReportSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportSuccess.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnWriteReportError(ctx context.Context, msg *WriteReportError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportError.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnWriteReportDuplicateTx(ctx context.Context, msg *WriteReportDuplicateTx) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportDuplicateTx.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnWriteReportTxFeeCalculationError(ctx context.Context, msg *WriteReportTxFeeCalculationError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportTxFeeCalculationError.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}
