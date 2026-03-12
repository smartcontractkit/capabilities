package monitoring

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"
)

// TelemetryContext wraps context for telemetry.
type TelemetryContext struct {
	TsStart int64
	capabilities.RequestMetadata
}

// Message defines the interface for telemetry messages.
type Message interface {
	proto.Message
	LogAttributes() []attribute.KeyValue
	MetricAttributes() []attribute.KeyValue
}

// ErrorMessage extends Message with error details.
type ErrorMessage interface {
	Message
	GetSummary() string
	GetCause() string
}

// MessageBuilder constructs telemetry messages with shared execution context.
type MessageBuilder struct {
	ChainInfo   types.ChainInfo
	CapInfo     capabilities.CapabilityInfo
	nodeAddress string
}

// NewMessageBuilder creates a new MessageBuilder.
func NewMessageBuilder(chainInfo types.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{ChainInfo: chainInfo, CapInfo: capInfo, nodeAddress: nodeAddress}
}

// RequestLggr creates a logger enriched with execution context attributes.
func (m *MessageBuilder) RequestLggr(lggr logger.SugaredLogger, telemetryContext TelemetryContext) logger.SugaredLogger {
	attrs := m.BuildExecutionContext(telemetryContext).LogAttributes()
	return lggr.With(AttrsToErrorKV(attrs)...)
}

// BuildExecutionContext builds the shared ExecutionContext.
func (m *MessageBuilder) BuildExecutionContext(tc TelemetryContext) *capmonitoring.ExecutionContext {
	return &capmonitoring.ExecutionContext{
		MetaSourceId: m.nodeAddress,

		MetaChainFamilyName: m.ChainInfo.FamilyName,
		MetaChainId:         m.ChainInfo.ChainID,
		MetaNetworkName:     m.ChainInfo.NetworkName,
		MetaNetworkNameFull: m.ChainInfo.NetworkNameFull,

		MetaWorkflowId:               tc.WorkflowID,
		MetaWorkflowOwner:            tc.WorkflowOwner,
		MetaWorkflowExecutionId:      tc.WorkflowExecutionID,
		MetaWorkflowName:             tc.WorkflowName,
		MetaWorkflowDonId:            tc.WorkflowDonID,
		MetaWorkflowDonConfigVersion: tc.WorkflowDonConfigVersion,
		MetaReferenceId:              tc.ReferenceID,

		MetaCapabilityType: string(m.CapInfo.CapabilityType),
		MetaCapabilityId:   m.CapInfo.ID,
		// nolint:gosec // G115: integer overflow conversion
		MetaCapabilityTimestampStart: uint64(tc.TsStart),
		// nolint:gosec // G115: integer overflow conversion
		MetaCapabilityTimestampEmit: uint64(time.Now().UnixMilli()),
	}
}

// LogAndEmitSuccess logs a success message with attributes and emits via the beholder processor.
func LogAndEmitSuccess(
	ctx context.Context,
	successMessage string,
	lggr logger.Logger,
	beholderProcessor beholder.ProtoProcessor,
	m Message,
) {
	lggr.Infow(successMessage, AttrsToErrorKV(m.LogAttributes())...)
	if err := beholderProcessor.Process(ctx, m); err != nil {
		lggr.Errorw(fmt.Sprintf("Failed to process %s message", GetMessageName(m)), "err", err)
	}
}

// EmitInitiated emits an initiated event via the beholder processor.
func EmitInitiated(
	ctx context.Context,
	lggr logger.Logger,
	beholderProcessor beholder.ProtoProcessor,
	m proto.Message,
) {
	if err := beholderProcessor.Process(ctx, m); err != nil {
		lggr.Errorw(fmt.Sprintf("Failed to process %s message", GetMessageName(m)), "err", err)
	}
}

// LogAndEmitError logs an error (Warn for user errors, Error otherwise) and emits via the processor.
func LogAndEmitError(
	ctx context.Context,
	lggr logger.Logger,
	beholderProcessor beholder.ProtoProcessor,
	eM ErrorMessage,
) {
	localLogAttributes := eM.LogAttributes()
	for i := 0; i < len(localLogAttributes); i++ {
		if localLogAttributes[i].Key == "summary" {
			localLogAttributes = append(localLogAttributes[:i], localLogAttributes[i+1:]...)
			break
		}
	}

	logMsg := eM.GetSummary() + " err: " + eM.GetCause()
	kvs := AttrsToErrorKV(localLogAttributes)
	if userErrMsg, ok := eM.(interface{ GetIsUserError() bool }); ok && userErrMsg.GetIsUserError() {
		lggr.Warnw(logMsg, kvs...)
	} else {
		lggr.Errorw(logMsg, kvs...)
	}
	if err := beholderProcessor.Process(ctx, eM); err != nil {
		lggr.Errorw(fmt.Sprintf("Failed to process %s message", GetMessageName(eM)), "err", err)
	}
}

// AttrsToErrorKV converts a slice of KeyValue into a flat []any of alternating key/value for logger kvs.
func AttrsToErrorKV(attrs []attribute.KeyValue) []any {
	kvs := make([]any, 0, len(attrs)*2)
	for _, attr := range attrs {
		if !attr.Valid() {
			continue
		}
		kvs = append(kvs,
			string(attr.Key),
			attr.Value.AsInterface(),
		)
	}
	return kvs
}

// GetMessageName extracts the short message name from a proto message's schema full name.
func GetMessageName(r proto.Message) string {
	fullNameSplit := strings.Split(beholder.ToSchemaFullName(r), ".")
	return fullNameSplit[len(fullNameSplit)-1]
}
