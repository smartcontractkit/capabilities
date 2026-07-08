package monitoring

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"google.golang.org/protobuf/proto"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
)

func ns(name string) string { return fmt.Sprintf("stellar_capability_%s", name) }

type basicCapEmitMessage interface {
	GetExecutionContext() *capmonitoring.ExecutionContext
	MetricAttributes() []attribute.KeyValue
}

func newBasicCapMetric(metricName string, msg proto.Message) (capmonitoring.MetricsCapBasic, error) {
	info := capmonitoring.NewMetricsInfoCapBasic(ns(metricName), commonbeholder.ToSchemaFullName(msg))
	basic, err := capmonitoring.NewMetricsCapBasic(info)
	if err != nil {
		return capmonitoring.MetricsCapBasic{}, fmt.Errorf("failed to create %s metric: %w", metricName, err)
	}
	return basic, nil
}

func recordBasicCapEmit(ctx context.Context, basic capmonitoring.MetricsCapBasic, msg basicCapEmitMessage) {
	ec := msg.GetExecutionContext()
	basic.RecordEmit(ctx, ec.GetMetaCapabilityTimestampStart(), ec.GetMetaCapabilityTimestampEmit(), msg.MetricAttributes()...)
}

// Metrics holds the per-method instruments for Stellar capability operations.
type Metrics struct {
	ReadContractSuccess struct {
		basic capmonitoring.MetricsCapBasic
	}
	ReadContractError struct {
		basic capmonitoring.MetricsCapBasic
	}
	WriteReportSuccess struct {
		basic capmonitoring.MetricsCapBasic
	}
	WriteReportError struct {
		basic capmonitoring.MetricsCapBasic
	}
	WriteReportDuplicateTx struct {
		basic capmonitoring.MetricsCapBasic
	}
	WriteReportTxInfoRetrievalError struct {
		basic capmonitoring.MetricsCapBasic
	}
	WriteReportSuccessfulEarlyReturn struct {
		basic capmonitoring.MetricsCapBasic
	}
	WriteReportInvalidTransmissionState struct {
		basic capmonitoring.MetricsCapBasic
	}
	WriteReportTxHashRetrievalPhase struct {
		duration metric.Int64Histogram
	}
	WriteReportInvokeOnReportDuration struct {
		duration metric.Int64Histogram
	}
}

// NewMetrics constructs the Stellar capability metrics.
func NewMetrics() (Metrics, error) {
	m := Metrics{}
	var err error

	if m.ReadContractSuccess.basic, err = newBasicCapMetric("read_contract_success", &ReadContractSuccess{}); err != nil {
		return Metrics{}, err
	}
	if m.ReadContractError.basic, err = newBasicCapMetric("read_contract_error", &ReadContractError{}); err != nil {
		return Metrics{}, err
	}
	if m.WriteReportSuccess.basic, err = newBasicCapMetric("write_report_success", &WriteReportSuccess{}); err != nil {
		return Metrics{}, err
	}
	if m.WriteReportError.basic, err = newBasicCapMetric("write_report_error", &WriteReportError{}); err != nil {
		return Metrics{}, err
	}
	if m.WriteReportDuplicateTx.basic, err = newBasicCapMetric("write_report_duplicate_tx", &WriteReportDuplicateTx{}); err != nil {
		return Metrics{}, err
	}
	if m.WriteReportTxInfoRetrievalError.basic, err = newBasicCapMetric("write_report_tx_info_retrieval_error", &WriteReportTxInfoRetrievalError{}); err != nil {
		return Metrics{}, err
	}
	if m.WriteReportSuccessfulEarlyReturn.basic, err = newBasicCapMetric("write_report_successful_early_return", &WriteReportSuccessfulEarlyReturn{}); err != nil {
		return Metrics{}, err
	}
	if m.WriteReportInvalidTransmissionState.basic, err = newBasicCapMetric("write_report_invalid_transmission_state", &WriteReportInvalidTransmissionState{}); err != nil {
		return Metrics{}, err
	}

	meter := commonbeholder.GetMeter()
	txHashPhaseDuration := commonbeholder.MetricInfo{
		Name:        ns("write_report_tx_hash_retrieval_phase_duration_ms"),
		Unit:        "ms",
		Description: "The duration of Stellar WriteReport tx hash retrieval",
	}
	m.WriteReportTxHashRetrievalPhase.duration, err = txHashPhaseDuration.NewInt64Histogram(meter)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report tx hash retrieval phase duration metric: %w", err)
	}
	invokeOnReportDuration := commonbeholder.MetricInfo{
		Name:        ns("write_report_invoke_on_report_duration_ms"),
		Unit:        "ms",
		Description: "The duration of Stellar WriteReport InvokeOnReport calls by tx status",
	}
	m.WriteReportInvokeOnReportDuration.duration, err = invokeOnReportDuration.NewInt64Histogram(meter)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create write report invoke on report duration metric: %w", err)
	}

	return m, nil
}

func (m *Metrics) OnReadContractSuccess(ctx context.Context, msg *ReadContractSuccess) error {
	recordBasicCapEmit(ctx, m.ReadContractSuccess.basic, msg)
	return nil
}

func (m *Metrics) OnReadContractError(ctx context.Context, msg *ReadContractError) error {
	recordBasicCapEmit(ctx, m.ReadContractError.basic, msg)
	return nil
}

func (m *Metrics) OnWriteReportSuccess(ctx context.Context, msg *WriteReportSuccess) error {
	recordBasicCapEmit(ctx, m.WriteReportSuccess.basic, msg)
	return nil
}

func (m *Metrics) OnWriteReportError(ctx context.Context, msg *WriteReportError) error {
	recordBasicCapEmit(ctx, m.WriteReportError.basic, msg)
	return nil
}

func (m *Metrics) OnWriteReportDuplicateTx(ctx context.Context, msg *WriteReportDuplicateTx) error {
	recordBasicCapEmit(ctx, m.WriteReportDuplicateTx.basic, msg)
	return nil
}

func (m *Metrics) OnWriteReportTxInfoRetrievalError(ctx context.Context, msg *WriteReportTxInfoRetrievalError) error {
	recordBasicCapEmit(ctx, m.WriteReportTxInfoRetrievalError.basic, msg)
	return nil
}

func (m *Metrics) OnWriteReportSuccessfulEarlyReturn(ctx context.Context, msg *WriteReportSuccessfulEarlyReturn) error {
	recordBasicCapEmit(ctx, m.WriteReportSuccessfulEarlyReturn.basic, msg)
	return nil
}

func (m *Metrics) OnWriteReportInvalidTransmissionState(ctx context.Context, msg *WriteReportInvalidTransmissionState) error {
	recordBasicCapEmit(ctx, m.WriteReportInvalidTransmissionState.basic, msg)
	return nil
}

func (m *Metrics) OnWriteReportTxHashRetrievalPhase(ctx context.Context, msg *WriteReportTxHashRetrievalPhase) error {
	attrs := metric.WithAttributes(msg.MetricAttributes()...)
	m.WriteReportTxHashRetrievalPhase.duration.Record(ctx, msg.GetPhaseDurationMs(), attrs)
	return nil
}

func (m *Metrics) OnWriteReportInvokeOnReportDuration(ctx context.Context, msg *WriteReportInvokeOnReportDuration) error {
	attrs := metric.WithAttributes(msg.MetricAttributes()...)
	m.WriteReportInvokeOnReportDuration.duration.Record(ctx, msg.GetDurationMs(), attrs)
	return nil
}
