package monitoring

import (
	"encoding/hex"
	"fmt"
	"strconv"

	sdkpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

type TelemetryContext = commonmon.TelemetryContext
type Message = commonmon.Message
type ErrorMessage = commonmon.ErrorMessage

// MessageBuilder constructs telemetry messages for Aptos calls.
// Embeds common MessageBuilder for shared BuildExecutionContext and RequestLggr.
type MessageBuilder struct {
	*commonmon.MessageBuilder
}

// NewMessageBuilder creates a new Aptos-specific MessageBuilder.
func NewMessageBuilder(chainInfo types.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{
		MessageBuilder: commonmon.NewMessageBuilder(chainInfo, capInfo, nodeAddress),
	}
}

func (m *MessageBuilder) BuildWriteReportInitiated(tc TelemetryContext, req *aptoscap.WriteReportRequest) *WriteReportInitiated {
	return &WriteReportInitiated{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildViewInitiated(tc TelemetryContext, req *aptoscap.ViewRequest) *ViewInitiated {
	return &ViewInitiated{
		Req:              convertViewRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildViewSuccess(tc TelemetryContext, req *aptoscap.ViewRequest, responseLen uint64) *ViewSuccess {
	return &ViewSuccess{
		Req:              convertViewRequest(req),
		ResponseLen:      responseLen,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildViewError(tc TelemetryContext, req *aptoscap.ViewRequest, summary, cause string, isUserError bool) ErrorMessage {
	return &ViewError{
		Req:              convertViewRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
		IsUserError:      isUserError,
	}
}

func (m *MessageBuilder) BuildWriteReportSuccess(tc TelemetryContext, req *aptoscap.WriteReportRequest) *WriteReportSuccess {
	return &WriteReportSuccess{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportError(tc TelemetryContext, req *aptoscap.WriteReportRequest, summary, cause string, isUserError bool) ErrorMessage {
	return &WriteReportError{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
		IsUserError:      isUserError,
	}
}

func (m *MessageBuilder) BuildWriteReportTxFeeCalculationError(tc TelemetryContext, req *aptoscap.WriteReportRequest, txHash string, cause string) ErrorMessage {
	summary := "Failed to calculate transaction fee"
	if txHash != "" {
		summary = fmt.Sprintf("Failed to calculate transaction fee for tx: %s", txHash)
	}
	return &WriteReportTxFeeCalculationError{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
		TxHash:           txHash,
	}
}

func (m *MessageBuilder) BuildWriteReportDuplicateTx(tc TelemetryContext, req *aptoscap.WriteReportRequest, duplicateTxHash, successfulTxHash string) *WriteReportDuplicateTx {
	return &WriteReportDuplicateTx{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		DuplicateTxHash:  duplicateTxHash,
		SuccessfulTxHash: successfulTxHash,
	}
}

func (m *MessageBuilder) BuildWriteReportSuccessfulEarlyReturn(tc TelemetryContext) *WriteReportSuccessfulEarlyReturn {
	return &WriteReportSuccessfulEarlyReturn{
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportTransmitterMismatch(tc TelemetryContext, transmitter string, orderedTransmitters []string) *WriteReportTransmitterMismatch {
	return &WriteReportTransmitterMismatch{
		Transmitter:         transmitter,
		OrderedTransmitters: orderedTransmitters,
		ExecutionContext:    m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportP2pConfigIncomplete(tc TelemetryContext, position int32) *WriteReportP2PConfigIncomplete {
	return &WriteReportP2PConfigIncomplete{
		Position:         position,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func convertWriteReportRequest(req *aptoscap.WriteReportRequest) *WriteReportRequest {
	if req == nil {
		return nil
	}
	msg := &WriteReportRequest{
		Receiver:  req.Receiver,
		GasConfig: convertGasConfig(req.GasConfig),
	}
	if req.Report != nil {
		msg.Report = &ReportResponse{
			ConfigDigest:  req.Report.ConfigDigest,
			SeqNr:         req.Report.SeqNr,
			ReportContext: req.Report.ReportContext,
			RawReport:     req.Report.RawReport,
			Sigs:          convertAttributedSignature(req.Report.Sigs),
		}
	}
	return msg
}

func convertViewRequest(req *aptoscap.ViewRequest) *ViewRequest {
	if req == nil || req.Payload == nil || req.Payload.Module == nil {
		return nil
	}

	msg := &ViewRequest{
		ModuleAddress: req.Payload.Module.Address,
		ModuleName:    req.Payload.Module.Name,
		Function:      req.Payload.Function,
	}
	if req.LedgerVersion != nil {
		msg.RequestedLedgerVersion = req.LedgerVersion
	}
	return msg
}

func convertGasConfig(gc *aptoscap.GasConfig) *GasConfig {
	if gc == nil {
		return nil
	}
	return &GasConfig{
		MaxGasAmount: gc.MaxGasAmount,
		GasUnitPrice: gc.GasUnitPrice,
	}
}

func convertAttributedSignature(attributedSignatures []*sdkpb.AttributedSignature) []*AttributedSignature {
	convertedSignatures := []*AttributedSignature{}
	for _, as := range attributedSignatures {
		convertedSignatures = append(convertedSignatures, &AttributedSignature{
			Signature: as.Signature,
			SignerId:  as.SignerId,
		})
	}
	return convertedSignatures
}

// --- LogAttributes / MetricAttributes ---

func (r *WriteReportInitiated) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("receiver", bytesToHexOrPlaceholder(r.Req.GetReceiver(), "nil receiver")),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportInitiated) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *ViewInitiated) LogAttributes() []attribute.KeyValue {
	return append(viewRequestLogAttributes(r.Req), r.ExecutionContext.LogAttributes()...)
}

func (r *ViewInitiated) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *ViewSuccess) LogAttributes() []attribute.KeyValue {
	return append(append(viewRequestLogAttributes(r.Req), attribute.String("response_len", strconv.FormatUint(r.GetResponseLen(), 10))), r.ExecutionContext.LogAttributes()...)
}

func (r *ViewSuccess) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *ViewError) LogAttributes() []attribute.KeyValue {
	return append(append(viewRequestLogAttributes(r.Req),
		attribute.String("summary", r.GetSummary()),
		attribute.Bool("isUserError", r.GetIsUserError()),
	), r.ExecutionContext.LogAttributes()...)
}

func (r *ViewError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportSuccess) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("receiver", bytesToHexOrPlaceholder(r.Req.GetReceiver(), "nil receiver")),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportSuccess) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportError) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("receiver", bytesToHexOrPlaceholder(r.Req.GetReceiver(), "nil receiver")),
		attribute.String("summary", r.GetSummary()),
		attribute.Bool("isUserError", r.GetIsUserError()),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportTxFeeCalculationError) LogAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("receiver", bytesToHexOrPlaceholder(r.Req.GetReceiver(), "nil receiver")),
		attribute.String("summary", r.GetSummary()),
	}
	if r.GetTxHash() != "" {
		attrs = append(attrs, attribute.String("tx_hash", r.GetTxHash()))
	}
	return append(attrs, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportTxFeeCalculationError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportSuccessfulEarlyReturn) LogAttributes() []attribute.KeyValue {
	return r.ExecutionContext.LogAttributes()
}

func (r *WriteReportSuccessfulEarlyReturn) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportDuplicateTx) LogAttributes() []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("receiver", bytesToHexOrPlaceholder(r.Req.GetReceiver(), "nil receiver")),
	}
	if r.GetDuplicateTxHash() != "" {
		attrs = append(attrs, attribute.String("duplicate_tx_hash", r.GetDuplicateTxHash()))
	}
	if r.GetSuccessfulTxHash() != "" {
		attrs = append(attrs, attribute.String("successful_tx_hash", r.GetSuccessfulTxHash()))
	}
	return append(attrs, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportDuplicateTx) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportTransmitterMismatch) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("transmitter", r.GetTransmitter()),
		attribute.StringSlice("orderedTransmitters", r.GetOrderedTransmitters()),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportTransmitterMismatch) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportP2PConfigIncomplete) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.Int("position", int(r.GetPosition())),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportP2PConfigIncomplete) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func bytesToHexOrPlaceholder(value []byte, placeholder string) string {
	if value != nil {
		return hex.EncodeToString(value)
	}
	return placeholder
}

func viewRequestLogAttributes(req *ViewRequest) []attribute.KeyValue {
	if req == nil {
		return []attribute.KeyValue{
			attribute.String("module_address", "nil module"),
			attribute.String("module_name", ""),
			attribute.String("function", ""),
		}
	}

	attrs := []attribute.KeyValue{
		attribute.String("module_address", bytesToHexOrPlaceholder(req.GetModuleAddress(), "nil module")),
		attribute.String("module_name", req.GetModuleName()),
		attribute.String("function", req.GetFunction()),
	}
	if req.RequestedLedgerVersion != nil {
		attrs = append(attrs, attribute.String("requested_ledger_version", strconv.FormatUint(req.GetRequestedLedgerVersion(), 10)))
	}
	return attrs
}
