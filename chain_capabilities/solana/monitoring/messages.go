package monitoring

import (
	"fmt"

	solgo "github.com/gagliardetto/solana-go"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	sdkpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"go.opentelemetry.io/otel/attribute"

	"github.com/mr-tron/base58"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	solanacappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

type TelemetryContext = commonmon.TelemetryContext
type Message = commonmon.Message
type ErrorMessage = commonmon.ErrorMessage

// MessageBuilder constructs telemetry messages for Solana calls.
// Embeds common MessageBuilder for shared BuildExecutionContext and RequestLggr.
type MessageBuilder struct {
	*commonmon.MessageBuilder
}

// NewMessageBuilder creates a new Solana-specific MessageBuilder.
func NewMessageBuilder(chainInfo types.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{
		MessageBuilder: commonmon.NewMessageBuilder(chainInfo, capInfo, nodeAddress),
	}
}

func (m *MessageBuilder) BuildWriteReportTxFeeCalculationError(tc TelemetryContext, req *solcap.WriteReportRequest, signature solgo.Signature, cause string) ErrorMessage {
	summary := "Failed to calculate transaction fee"
	if !signature.IsZero() {
		summary = fmt.Sprintf("Failed to calculate transaction fee for tx: %s", signature)
	}
	return &WriteReportTxFeeCalculationError{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
	}
}

func (m *MessageBuilder) BuildLogTriggerInitiated(tc TelemetryContext, req *solanacappb.FilterLogTriggerRequest) *LogTriggerInitiated {
	return &LogTriggerInitiated{Req: logTriggerRequestToMonitoring(req), ExecutionContext: m.BuildExecutionContext(tc)}
}

func logTriggerRequestToMonitoring(req *solanacappb.FilterLogTriggerRequest) *FilterLogTriggerRequest {
	if req == nil {
		return nil
	}

	return &FilterLogTriggerRequest{
		Address:   req.Address,
		EventName: req.EventName,
	}
}

func (m *MessageBuilder) BuildLogTriggerSuccess(tc TelemetryContext, triggerID string, req *solanacappb.FilterLogTriggerRequest, logCount int, latestOffsetBlock int64) Message {
	return &LogTriggerSuccess{
		TriggerId:         triggerID,
		Req:               logTriggerRequestToMonitoring(req),
		LogCount:          int32(logCount), // nolint:gosec // G115: integer overflow conversion int -> int32 (gosec)
		LatestOffsetBlock: latestOffsetBlock,
		ExecutionContext:  m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildLogTriggerError(tc TelemetryContext, triggerID string, summary, cause string) ErrorMessage {
	return &LogTriggerError{
		TriggerId:        triggerID,
		Summary:          summary,
		Cause:            cause,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildLogTriggerCleanUpError(tc TelemetryContext, summary, cause string) ErrorMessage {
	return &LogTriggerCleanUpError{
		Summary:          summary,
		Cause:            cause,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildLogTriggerEventDroppedError(tc TelemetryContext, triggerID string, log *solana.Log, summary, cause string, isLimitError bool) ErrorMessage {
	return &LogTriggerEventDroppedError{
		TriggerId:        triggerID,
		TxHash:           base58.Encode(log.TxHash[:]),
		BlockHash:        base58.Encode(log.BlockHash[:]),
		LogIndex:         log.LogIndex,
		Summary:          summary,
		Cause:            cause,
		IsLimitError:     isLimitError,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

// LogAttributes implementations for each message type

func (r *LogTriggerSuccess) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("trigger_id", r.GetTriggerId()),
		attribute.Int64("log_count", int64(r.GetLogCount())),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *LogTriggerSuccess) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *LogTriggerError) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("trigger_id", r.GetTriggerId()),
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *LogTriggerError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *LogTriggerCleanUpError) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("summary", r.GetSummary()),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *LogTriggerCleanUpError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (r *LogTriggerEventDroppedError) LogAttributes() []attribute.KeyValue {
	return append([]attribute.KeyValue{
		attribute.String("trigger_id", r.GetTriggerId()),
		attribute.String("tx_hash", r.GetTxHash()),
		attribute.String("block_hash", r.GetBlockHash()),
		attribute.Int64("log_index", r.GetLogIndex()),
		attribute.String("summary", r.GetSummary()),
		attribute.Bool("is_limit_error", r.GetIsLimitError()),
	}, r.ExecutionContext.LogAttributes()...)
}

func (r *LogTriggerEventDroppedError) MetricAttributes() []attribute.KeyValue {
	return r.ExecutionContext.MetricsAttributes()
}

func (m *MessageBuilder) BuildWriteReportInitiated(tc TelemetryContext, req *solanacappb.WriteReportRequest) *WriteReportInitiated {
	return &WriteReportInitiated{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc)}
}

func convertWriteReportRequest(req *solanacappb.WriteReportRequest) *WriteReportRequest {
	return &WriteReportRequest{
		Receiver:          req.Receiver,
		RemainingAccounts: convertRemainingAccounts(req.RemainingAccounts),
		Report: &ReportResponse{
			ConfigDigest:  req.Report.ConfigDigest,
			SeqNr:         req.Report.SeqNr,
			ReportContext: req.Report.ReportContext,
			RawReport:     req.Report.RawReport,
			Sigs:          convertAttributedSignature(req.Report.Sigs),
		},
		ComputeConfig: &ComputeConfig{
			ComputeLimit:    req.GetComputeConfig().GetComputeLimit(),
			ComputeMaxPrice: req.GetComputeConfig().GetComputeMaxPrice(),
		},
	}
}

func convertRemainingAccounts(accs []*solanacappb.AccountMeta) []*AccountMeta {
	ret := []*AccountMeta{}
	for _, acc := range accs {
		ret = append(ret, &AccountMeta{
			PublicKey:  acc.PublicKey,
			IsWritable: acc.IsWritable,
		})
	}
	return ret
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

func (m *MessageBuilder) BuildWriteReportSuccess(tc TelemetryContext, req *solanacappb.WriteReportRequest) *WriteReportSuccess {
	return &WriteReportSuccess{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportError(tc TelemetryContext, req *solanacappb.WriteReportRequest, summary, cause string, isUserError bool) ErrorMessage {
	return &WriteReportError{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
		IsUserError:      isUserError,
	}
}
