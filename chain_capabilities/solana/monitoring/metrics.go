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
	LogTriggerSuccess struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	LogTriggerError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	LogTriggerCleanUpError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
	LogTriggerEventDroppedError struct {
		basic commoncapbeholder.MetricsCapBasic
	}
}

// NewMetrics constructs all counters & histograms bound to a given chainID
func NewMetrics() (Metrics, error) {
	m := Metrics{}
	var err error

	// -- LogTrigger --
	ltSuccess := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_success"), commonbeholder.ToSchemaFullName(&LogTriggerSuccess{}))
	m.LogTriggerSuccess.basic, err = commoncapbeholder.NewMetricsCapBasic(ltSuccess)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger success metric: %w", err)
	}
	ltErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_error"), commonbeholder.ToSchemaFullName(&LogTriggerError{}))
	m.LogTriggerError.basic, err = commoncapbeholder.NewMetricsCapBasic(ltErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger error metric: %w", err)
	}
	ltcuErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_clean_up_error"), commonbeholder.ToSchemaFullName(&LogTriggerCleanUpError{}))
	m.LogTriggerCleanUpError.basic, err = commoncapbeholder.NewMetricsCapBasic(ltcuErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger clean up error metric: %w", err)
	}
	ltedErr := commoncapbeholder.NewMetricsInfoCapBasic(ns("log_trigger_event_dropped_error"), commonbeholder.ToSchemaFullName(&LogTriggerEventDroppedError{}))
	m.LogTriggerEventDroppedError.basic, err = commoncapbeholder.NewMetricsCapBasic(ltedErr)
	if err != nil {
		return Metrics{}, fmt.Errorf("failed to create log trigger event dropped error metric: %w", err)
	}
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

// -- LogTrigger --

func (m *Metrics) OnLogTriggerSuccess(ctx context.Context, msg *LogTriggerSuccess) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerSuccess.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnLogTriggerError(ctx context.Context, msg *LogTriggerError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerError.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnLogTriggerCleanUpError(ctx context.Context, msg *LogTriggerCleanUpError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerCleanUpError.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (m *Metrics) OnTriggerEventDroppedError(ctx context.Context, msg *LogTriggerEventDroppedError) error {
	start, emit := msg.ExecutionContext.MetaCapabilityTimestampStart, msg.ExecutionContext.MetaCapabilityTimestampEmit
	m.LogTriggerEventDroppedError.basic.RecordEmit(ctx, start, emit, msg.MetricAttributes()...)
	return nil
}

func (r *LogTriggerInitiated) LogAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{}
	if r.Req != nil {
		attrs = append(attrs,
			attribute.String("event_name", r.Req.GetEventName()),
			attribute.Int64("starting_block", r.Req.GetStartingBlock()),
		)
	}
	return append(attrs, r.ExecutionContext.LogAttributes()...)
}

func (r *LogTriggerInitiated) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
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

func (r *WriteReportTxFeeCalculationError) LogAttributes() []attribute.KeyValue {
	attributes := []attribute.KeyValue{
		attribute.String("receiver", getReceiver(r.Req.GetReceiver())),
		attribute.String("summary", r.GetSummary()),
	}

	return append(attributes, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportTxFeeCalculationError) MetricAttributes() []attribute.KeyValue {
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
