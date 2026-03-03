package monitoring

import (
	"fmt"
	"time"

	solgo "github.com/gagliardetto/solana-go"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	sdkpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/mr-tron/base58"
	solanacappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
)

// TelemetryContext wraps context for telemetry
type TelemetryContext struct {
	TsStart int64
	capabilities.RequestMetadata
}

// MessageBuilder constructs telemetry messages for Solana calls
type MessageBuilder struct {
	ChainInfo   types.ChainInfo
	CapInfo     capabilities.CapabilityInfo
	nodeAddress string
}

// NewMessageBuilder creates a new builder
func NewMessageBuilder(chainInfo types.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{ChainInfo: chainInfo, CapInfo: capInfo, nodeAddress: nodeAddress}
}

func (m *MessageBuilder) RequestLggr(lggr logger.SugaredLogger, telemetryContext TelemetryContext) logger.SugaredLogger {
	attrs := m.BuildExecutionContext(telemetryContext).LogAttributes()
	lggrAttrs := attrsToErrorKV(attrs)
	return lggr.With(lggrAttrs...)
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

type Message interface {
	proto.Message
	// LogAttributes - Defines list of key value pairs to be included in a log line for the Message
	LogAttributes() []attribute.KeyValue
	// MetricAttributes - defines a subset of Attributes key value pairs to be added as labels to metrics that correspond to the Message
	// *MUST NOT* include high cardinality values as it will *KILL* metrics collector.
	MetricAttributes() []attribute.KeyValue
}

type ErrorMessage interface {
	Message
	GetSummary() string
	GetCause() string
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

// BuildExecutionContext builds the shared ExecutionContext
func (m *MessageBuilder) BuildExecutionContext(tc TelemetryContext) *capmonitoring.ExecutionContext {
	ex := &capmonitoring.ExecutionContext{
		MetaSourceId: m.nodeAddress,

		// Chain
		MetaChainFamilyName: m.ChainInfo.FamilyName,
		MetaChainId:         m.ChainInfo.ChainID,
		MetaNetworkName:     m.ChainInfo.NetworkName,
		MetaNetworkNameFull: m.ChainInfo.NetworkNameFull,

		// Workflow
		MetaWorkflowId:               tc.WorkflowID,
		MetaWorkflowOwner:            tc.WorkflowOwner,
		MetaWorkflowExecutionId:      tc.WorkflowExecutionID,
		MetaWorkflowName:             tc.WorkflowName,
		MetaWorkflowDonId:            tc.WorkflowDonID,
		MetaWorkflowDonConfigVersion: tc.WorkflowDonConfigVersion,
		MetaReferenceId:              tc.ReferenceID,
		// Capability
		MetaCapabilityType: string(m.CapInfo.CapabilityType),
		MetaCapabilityId:   m.CapInfo.ID,
		// G115: integer overflow conversion uint64 -> int64 (gosec)
		// nolint:gosec
		MetaCapabilityTimestampStart: uint64(tc.TsStart),
		// G115: integer overflow conversion uint64 -> int64 (gosec)
		// nolint:gosec
		MetaCapabilityTimestampEmit: uint64(time.Now().UnixMilli()),
	}
	return ex
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
