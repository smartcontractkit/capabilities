package monitoring

import (
	"time"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	sdkpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"
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

func (m *MessageBuilder) BuildWriteReportInitiated(tc TelemetryContext, req *solcap.WriteReportRequest) *WriteReportInitiated {
	return &WriteReportInitiated{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc)}
}

func convertWriteReportRequest(req *solcap.WriteReportRequest) *WriteReportRequest {
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

func convertRemainingAccounts(accs []*solcap.AccountMeta) []*AccountMeta {
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

func (m *MessageBuilder) BuildWriteReportSuccess(tc TelemetryContext, req *solcap.WriteReportRequest) *WriteReportSuccess {
	return &WriteReportSuccess{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportError(tc TelemetryContext, req *solcap.WriteReportRequest, summary, cause string, isUserError bool) ErrorMessage {
	return &WriteReportError{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
		IsUserError:      isUserError,
	}
}
