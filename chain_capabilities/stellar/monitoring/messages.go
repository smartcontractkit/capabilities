package monitoring

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	capmon "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/monitoring"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

type TelemetryContext = commonmon.TelemetryContext
type Message = commonmon.Message
type ErrorMessage = commonmon.ErrorMessage

// MessageBuilder constructs telemetry messages for Stellar calls.
// Embeds the common MessageBuilder for shared BuildExecutionContext and RequestLggr.
type MessageBuilder struct {
	*commonmon.MessageBuilder
}

// NewMessageBuilder creates a new Stellar-specific MessageBuilder.
func NewMessageBuilder(chainInfo types.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{
		MessageBuilder: commonmon.NewMessageBuilder(chainInfo, capInfo, nodeAddress),
	}
}

// CapabilityMetricsAttributes returns capability-scoped OTel labels for v2 action metrics.
func (m *MessageBuilder) CapabilityMetricsAttributes() []attribute.KeyValue {
	return []attribute.KeyValue{
		attribute.String(capmon.LabelChainFamilyName, capmon.ValOrUnknown(m.ChainInfo.FamilyName)),
		attribute.String(capmon.LabelChainID, capmon.ValOrUnknown(m.ChainInfo.ChainID)),
		attribute.String(capmon.LabelNetworkName, capmon.ValOrUnknown(m.ChainInfo.NetworkName)),
		attribute.String(capmon.LabelNetworkNameFull, capmon.ValOrUnknown(m.ChainInfo.NetworkNameFull)),
		attribute.String(capmon.LabelCapabilityType, capmon.ValOrUnknown(string(m.CapInfo.CapabilityType))),
		attribute.String(capmon.LabelCapabilityID, capmon.ValOrUnknown(m.CapInfo.ID)),
	}
}

func (m *MessageBuilder) BuildWriteReportTxInfoRetrievalError(tc TelemetryContext, req *stellarcap.WriteReportRequest, txHash, cause string) ErrorMessage {
	summary := "Failed to retrieve transaction info"
	if txHash != "" {
		summary = fmt.Sprintf("Failed to retrieve transaction info for tx: %s", txHash)
	}
	return &WriteReportTxInfoRetrievalError{
		Req:              convertWriteReportRequest(req),
		Summary:          summary,
		Cause:            cause,
		TxHash:           txHash,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportInvalidTransmissionState(
	tc TelemetryContext,
	req *stellarcap.WriteReportRequest,
	transmissionState uint32,
	invalidReceiver, success bool,
	transmissionID, transmitter, summary, cause string,
) ErrorMessage {
	return &WriteReportInvalidTransmissionState{
		Req:               convertWriteReportRequest(req),
		Summary:           summary,
		Cause:             cause,
		TransmissionState: transmissionState,
		InvalidReceiver:   invalidReceiver,
		Success:           success,
		TransmissionId:    transmissionID,
		Transmitter:       transmitter,
		ExecutionContext:  m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportDuplicateTx(tc TelemetryContext, req *stellarcap.WriteReportRequest, duplicateTxHash, canonicalTxHash string) *WriteReportDuplicateTx {
	return &WriteReportDuplicateTx{
		Req:              convertWriteReportRequest(req),
		DuplicateTxHash:  duplicateTxHash,
		CanonicalTxHash:  canonicalTxHash,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportSuccessfulEarlyReturn(tc TelemetryContext) *WriteReportSuccessfulEarlyReturn {
	return &WriteReportSuccessfulEarlyReturn{
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func convertWriteReportRequest(req *stellarcap.WriteReportRequest) *WriteReportRequest {
	if req == nil {
		return nil
	}
	msg := &WriteReportRequest{
		ContractId: req.GetContractId(),
	}
	if req.Report != nil {
		msg.ReportSize = uint64(len(req.Report.GetRawReport()))
		msg.SigsCount = uint32(len(req.Report.GetSigs())) //nolint:gosec // sig count is bounded by DON size
	}
	return msg
}

func writeReportRequestLogAttributes(req *WriteReportRequest) []attribute.KeyValue {
	if req == nil {
		return []attribute.KeyValue{attribute.String("contract_id", "nil request")}
	}
	return []attribute.KeyValue{
		attribute.String("contract_id", req.GetContractId()),
		attribute.Int64("report_size", int64(req.GetReportSize())), //nolint:gosec // G115: report size is bounded by capability limit
		attribute.Int64("sigs_count", int64(req.GetSigsCount())),
	}
}

func (r *WriteReportTxInfoRetrievalError) LogAttributes() []attribute.KeyValue {
	attrs := append(writeReportRequestLogAttributes(r.Req),
		attribute.String("summary", r.GetSummary()),
	)
	if r.GetTxHash() != "" {
		attrs = append(attrs, attribute.String("tx_hash", r.GetTxHash()))
	}
	return append(attrs, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportTxInfoRetrievalError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportInvalidTransmissionState) LogAttributes() []attribute.KeyValue {
	return append(append(writeReportRequestLogAttributes(r.Req),
		attribute.String("summary", r.GetSummary()),
		attribute.Int64("transmission_state", int64(r.GetTransmissionState())),
		attribute.Bool("invalid_receiver", r.GetInvalidReceiver()),
		attribute.Bool("success", r.GetSuccess()),
		attribute.String("transmission_id", r.GetTransmissionId()),
		attribute.String("transmitter", r.GetTransmitter()),
	), r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportInvalidTransmissionState) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportSuccessfulEarlyReturn) LogAttributes() []attribute.KeyValue {
	return r.ExecutionContext.LogAttributes()
}

func (r *WriteReportSuccessfulEarlyReturn) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportDuplicateTx) LogAttributes() []attribute.KeyValue {
	attrs := writeReportRequestLogAttributes(r.Req)
	if r.GetDuplicateTxHash() != "" {
		attrs = append(attrs, attribute.String("duplicate_tx_hash", r.GetDuplicateTxHash()))
	}
	if r.GetCanonicalTxHash() != "" {
		attrs = append(attrs, attribute.String("canonical_tx_hash", r.GetCanonicalTxHash()))
	}
	return append(attrs, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportDuplicateTx) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}
