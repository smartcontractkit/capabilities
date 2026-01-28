package monitoring

import (
	"context"
	"fmt"

	"github.com/gagliardetto/solana-go"
	"go.opentelemetry.io/otel/attribute"

	commoncapbeholder "github.com/smartcontractkit/capabilities/libs/monitoring"

	commonbeholder "github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

func ns(name string) string { return fmt.Sprintf("sol_capability_%s", name) }

// Metrics holds all per-method instruments
type Metrics struct {
	WriteReportSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	WriteReportError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
}

// NewMetrics constructs all counters & histograms bound to a given chainID
func NewMetrics() (Metrics, error) {
	m := Metrics{}
	var err error

	// -- WriteReport --
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

	return m, nil
}

// -- WriteReport --
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

func (r *WriteReportSuccess) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("receiver", getReceiver(r.Req.GetReceiver())),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportSuccess) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportError) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("receiver", getReceiver(r.Req.GetReceiver())),
		attribute.String("summary", r.GetSummary()),
		attribute.Bool("isUserError", r.GetIsUserError()),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func getReceiver(receiver []byte) string {
	if receiver != nil {
		if len(receiver) != solana.PublicKeyLength {
			return fmt.Sprintf("invalid length receiver: %d", len(receiver))
		}
		key := solana.PublicKeyFromBytes(receiver)
		return key.String()
	}
	return "nil receiver"
}
