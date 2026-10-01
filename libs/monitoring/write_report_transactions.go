package monitoring

import (
	"go.opentelemetry.io/otel/attribute"
)

// LogAttributes returns the execution-context attributes plus the tx hashes for logging.
// It implements the chain_capabilities/common/monitoring Message interface so the shared
// LogAndEmitSuccess helper can log and emit this message.
func (x *WriteReportTransactions) LogAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{}
	if ec := x.GetExecutionContext(); ec != nil {
		attrs = append(attrs, ec.LogAttributes()...)
	}
	return append(attrs, attribute.Int("tx_hashes_count", len(x.GetTxHashes())))
}

// MetricAttributes returns the execution-context metric attributes. The tx hashes
// themselves are deliberately excluded to avoid high-cardinality metric labels.
func (x *WriteReportTransactions) MetricAttributes() []attribute.KeyValue {
	if ec := x.GetExecutionContext(); ec != nil {
		return ec.MetricsAttributes()
	}
	return nil
}
