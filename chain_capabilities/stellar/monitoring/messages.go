package monitoring

import (
	"fmt"

	"go.opentelemetry.io/otel/attribute"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
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

func (m *MessageBuilder) BuildReadContractInitiated(tc commonmon.TelemetryContext, req stellartypes.SimulateTransactionRequest) *ReadContractInitiated {
	return &ReadContractInitiated{
		Req:              convertReadContractRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildReadContractSuccess(tc commonmon.TelemetryContext, req stellartypes.SimulateTransactionRequest, resultLen uint64, ledgerSequence uint32) *ReadContractSuccess {
	return &ReadContractSuccess{
		Req:              convertReadContractRequest(req),
		ResultLen:        resultLen,
		LedgerSequence:   ledgerSequence,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildReadContractError(tc commonmon.TelemetryContext, req stellartypes.SimulateTransactionRequest, summary string, err caperrors.Error) commonmon.ErrorMessage {
	return &ReadContractError{
		Req:              convertReadContractRequest(req),
		Summary:          summary,
		Cause:            err.Error(),
		IsUserError:      err.Origin() == caperrors.OriginUser,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportInitiated(tc TelemetryContext, req *stellarcap.WriteReportRequest) *WriteReportInitiated {
	return &WriteReportInitiated{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportSuccess(tc TelemetryContext, req *stellarcap.WriteReportRequest) *WriteReportSuccess {
	return &WriteReportSuccess{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportError(tc TelemetryContext, req *stellarcap.WriteReportRequest, summary string, err caperrors.Error) ErrorMessage {
	return &WriteReportError{
		Req:              convertWriteReportRequest(req),
		Summary:          summary,
		Cause:            err.Error(),
		IsUserError:      err.Origin() == caperrors.OriginUser,
		ExecutionContext: m.BuildExecutionContext(tc),
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

// convertReadContractRequest extracts the non-sensitive subset of the request for telemetry
// (raw argument values are intentionally omitted; only the count is recorded).
func convertReadContractRequest(req stellartypes.SimulateTransactionRequest) *ReadContractRequest {
	return &ReadContractRequest{
		ContractId:    req.ContractID,
		Function:      req.Function,
		ArgsCount:     uint64(len(req.Args)),
		SourceAccount: req.SourceAccount,
	}
}

func appendUserErrorLogAttributes(reqAttrs []attribute.KeyValue, summary string, isUserError bool, ec *capmonitoring.ExecutionContext) []attribute.KeyValue {
	return append(append(reqAttrs,
		attribute.String("summary", summary),
		attribute.Bool("is_user_error", isUserError),
	), ec.LogAttributes()...)
}

func readContractRequestLogAttributes(req *ReadContractRequest) []attribute.KeyValue {
	if req == nil {
		return []attribute.KeyValue{
			attribute.String("contract_id", "nil request"),
			attribute.String("function", ""),
		}
	}
	attrs := []attribute.KeyValue{
		attribute.String("contract_id", req.GetContractId()),
		attribute.String("function", req.GetFunction()),
		attribute.Int64("args_count", int64(req.GetArgsCount())), //nolint:gosec // G115: arg count is bounded by request size
	}
	if req.GetSourceAccount() != "" {
		attrs = append(attrs, attribute.String("source_account", req.GetSourceAccount()))
	}
	return attrs
}

func (r *ReadContractInitiated) LogAttributes() []attribute.KeyValue {
	return append(readContractRequestLogAttributes(r.Req), r.ExecutionContext.LogAttributes()...)
}

func (r *ReadContractInitiated) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *ReadContractSuccess) LogAttributes() []attribute.KeyValue {
	return append(append(readContractRequestLogAttributes(r.Req),
		attribute.Int64("result_len", int64(r.GetResultLen())), //nolint:gosec // G115: result length is bounded by simulation output
		attribute.Int64("ledger_sequence", int64(r.GetLedgerSequence())),
	), r.ExecutionContext.LogAttributes()...)
}

func (r *ReadContractSuccess) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *ReadContractError) LogAttributes() []attribute.KeyValue {
	return appendUserErrorLogAttributes(readContractRequestLogAttributes(r.Req), r.GetSummary(), r.GetIsUserError(), r.ExecutionContext)
}

func (r *ReadContractError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
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

func (r *WriteReportInitiated) LogAttributes() []attribute.KeyValue {
	return append(writeReportRequestLogAttributes(r.Req), r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportInitiated) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportSuccess) LogAttributes() []attribute.KeyValue {
	return append(writeReportRequestLogAttributes(r.Req), r.ExecutionContext.LogAttributes()...)
}

func (r *WriteReportSuccess) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *WriteReportError) LogAttributes() []attribute.KeyValue {
	return appendUserErrorLogAttributes(writeReportRequestLogAttributes(r.Req), r.GetSummary(), r.GetIsUserError(), r.ExecutionContext)
}

func (r *WriteReportError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
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
