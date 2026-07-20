package monitoring

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
)

// capDurationBucketBoundariesMs covers the full lifecycle of a Stellar write-report:
// from fast read-contract calls (~10ms) through ledger confirmation wait times
// (~5,000-30,000ms) and up to the max-retry timeout (~60,000ms).
var capDurationBucketBoundariesMs = []float64{
	10, 25, 50, 100, 250, 500,
	1000, 2500, 5000,
	10000, 20000, 30000, 60000,
}

var newMetricsCapBasic = capmonitoring.NewMetricsCapBasic

func ns(name string) string { return fmt.Sprintf("stellar_capability_%s", name) }

type basicCapEmitMessage interface {
	GetExecutionContext() *capmonitoring.ExecutionContext
	MetricAttributes() []attribute.KeyValue
}

func newBasicCapMetric(metricName string, msg proto.Message) (capmonitoring.MetricsCapBasic, error) {
	info := capmonitoring.NewMetricsInfoCapBasicWithBuckets(
		ns(metricName),
		commonbeholder.ToSchemaFullName(msg),
		capDurationBucketBoundariesMs,
	)
	basic, err := newMetricsCapBasic(info)
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
