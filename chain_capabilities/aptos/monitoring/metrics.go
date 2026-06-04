package monitoring

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"

	commoncapbeholder "github.com/smartcontractkit/capabilities/libs/monitoring"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

func ns(name string) string { return fmt.Sprintf("apt_capability_%s", name) }

// Metrics holds all per-method instruments
type Metrics struct {
	ViewSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	ViewError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
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
	WriteReportSuccessfulEarlyReturn struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportTransmitterMismatch struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportP2PConfigIncomplete struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportTxInfoRetrievalPhase struct {
		count         metric.Int64Counter
		phaseDuration metric.Int64Histogram
	}
	WriteReportInvokeOnReportDuration struct {
		duration metric.Int64Histogram
	}
}

// NewMetrics constructs all counters & histograms
func NewMetrics() (Metrics, error) {
	m := Metrics{}
	var err error

	viewSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("view_success"), commonbeholder.ToSchemaFullName(&ViewSuccess{}))
	m.ViewSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(viewSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create view success metric: %w", err)
	}

	viewErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("view_error"), commonbeholder.ToSchemaFullName(&ViewError{}))
	m.ViewError.basic, err = commoncapbeholder.NewMetricsCapBasic(viewErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create view error metric: %w", err)
	}

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

	wrEarlyReturn := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_successful_early_return"), commonbeholder.ToSchemaFullName(&WriteReportSuccessfulEarlyReturn{}))
	m.WriteReportSuccessfulEarlyReturn.basic, err = commoncapbeholder.NewMetricsCapBasic(wrEarlyReturn)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report successful early return metric: %w", err)
	}

	wrTransmitterMismatch := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_transmitter_mismatch"), commonbeholder.ToSchemaFullName(&WriteReportTransmitterMismatch{}))
	m.WriteReportTransmitterMismatch.basic, err = commoncapbeholder.NewMetricsCapBasic(wrTransmitterMismatch)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report transmitter mismatch metric: %w", err)
	}

	wrP2pIncomplete := commoncapbeholder.NewMetricsInfoCapBasic(ns("write_report_p2p_config_incomplete"), commonbeholder.ToSchemaFullName(&WriteReportP2PConfigIncomplete{}))
	m.WriteReportP2PConfigIncomplete.basic, err = commoncapbeholder.NewMetricsCapBasic(wrP2pIncomplete)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report p2p config incomplete metric: %w", err)
	}

	meter := commonbeholder.GetMeter()
	txInfoPhaseCount := commonbeholder.MetricInfo{
		Name:        ns("write_report_tx_info_retrieval_phase_count"),
		Unit:        "",
		Description: "The count of Aptos WriteReport tx info retrieval phases by lookup type, phase, and result",
	}
	m.WriteReportTxInfoRetrievalPhase.count, err = txInfoPhaseCount.NewInt64Counter(meter)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report tx info retrieval phase count metric: %w", err)
	}
	txInfoPhaseDuration := commonbeholder.MetricInfo{
		Name:        ns("write_report_tx_info_retrieval_phase_duration_ms"),
		Unit:        "ms",
		Description: "The duration of Aptos WriteReport tx info retrieval phases by lookup type, phase, and result",
	}
	m.WriteReportTxInfoRetrievalPhase.phaseDuration, err = txInfoPhaseDuration.NewInt64Histogram(meter)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report tx info retrieval phase duration metric: %w", err)
	}
	invokeOnReportDuration := commonbeholder.MetricInfo{
		Name:        ns("write_report_invoke_on_report_duration_ms"),
		Unit:        "ms",
		Description: "The duration of Aptos WriteReport InvokeOnReport calls by tx status",
	}
	m.WriteReportInvokeOnReportDuration.duration, err = invokeOnReportDuration.NewInt64Histogram(meter)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report invoke on report duration metric: %w", err)
	}

	return m, nil
}

func (m *Metrics) OnViewSuccess(ctx context.Context, msg *ViewSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.ViewSuccess.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnViewError(ctx context.Context, msg *ViewError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.ViewError.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
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

func (m *Metrics) OnWriteReportSuccessfulEarlyReturn(ctx context.Context, msg *WriteReportSuccessfulEarlyReturn) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportSuccessfulEarlyReturn.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnWriteReportTransmitterMismatch(ctx context.Context, msg *WriteReportTransmitterMismatch) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportTransmitterMismatch.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnWriteReportP2PConfigIncomplete(ctx context.Context, msg *WriteReportP2PConfigIncomplete) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.WriteReportP2PConfigIncomplete.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnWriteReportTxInfoRetrievalPhase(ctx context.Context, msg *WriteReportTxInfoRetrievalPhase) error {
	attrs := metric.WithAttributes(msg.MetricAttributes()...)
	m.WriteReportTxInfoRetrievalPhase.count.Add(ctx, 1, attrs)
	m.WriteReportTxInfoRetrievalPhase.phaseDuration.Record(ctx, msg.GetPhaseDurationMs(), attrs)
	return nil
}

func (m *Metrics) OnWriteReportInvokeOnReportDuration(ctx context.Context, msg *WriteReportInvokeOnReportDuration) error {
	attrs := metric.WithAttributes(msg.MetricAttributes()...)
	m.WriteReportInvokeOnReportDuration.duration.Record(ctx, msg.GetDurationMs(), attrs)
	return nil
}
