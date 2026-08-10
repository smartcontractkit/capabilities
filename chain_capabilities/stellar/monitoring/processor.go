package monitoring

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

type metricsHandler interface {
	OnReadContractSuccess(context.Context, *ReadContractSuccess) error
	OnReadContractError(context.Context, *ReadContractError) error
	OnWriteReportSuccess(context.Context, *WriteReportSuccess) error
	OnWriteReportError(context.Context, *WriteReportError) error
	OnWriteReportTxInfoRetrievalError(context.Context, *WriteReportTxInfoRetrievalError) error
	OnWriteReportDuplicateTx(context.Context, *WriteReportDuplicateTx) error
	OnWriteReportSuccessfulEarlyReturn(context.Context, *WriteReportSuccessfulEarlyReturn) error
	OnWriteReportInvalidTransmissionState(context.Context, *WriteReportInvalidTransmissionState) error
}

// Processor dispatches telemetry messages to metrics and logs for Stellar operations.
type Processor struct {
	Metrics metricsHandler
	Lggr    logger.Logger
}

func (p *Processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	switch msg := m.(type) {
	case *ReadContractInitiated, *WriteReportInitiated:
		p.logMessage(m)
	case *ReadContractSuccess:
		p.logMessage(msg)
		if err := p.Metrics.OnReadContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish ReadContractSuccess metrics: %w", err)
		}
	case *ReadContractError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.Metrics.OnReadContractError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish ReadContractError metrics: %w", err)
			}
		}
	case *WriteReportSuccess:
		p.logMessage(msg)
		if err := p.Metrics.OnWriteReportSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccess metrics: %w", err)
		}
	case *WriteReportError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.Metrics.OnWriteReportError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish WriteReportError metrics: %w", err)
			}
		}
	case *WriteReportTxInfoRetrievalError:
		p.logMessage(msg)
		if err := p.Metrics.OnWriteReportTxInfoRetrievalError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTxInfoRetrievalError metrics: %w", err)
		}
	case *WriteReportDuplicateTx:
		p.logMessage(msg)
		if err := p.Metrics.OnWriteReportDuplicateTx(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportDuplicateTx metrics: %w", err)
		}
	case *WriteReportSuccessfulEarlyReturn:
		p.logMessage(msg)
		if err := p.Metrics.OnWriteReportSuccessfulEarlyReturn(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccessfulEarlyReturn metrics: %w", err)
		}
	case *WriteReportInvalidTransmissionState:
		p.logMessage(msg)
		if err := p.Metrics.OnWriteReportInvalidTransmissionState(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportInvalidTransmissionState metrics: %w", err)
		}
	default:
		return nil
	}
	return nil
}

func (p *Processor) logMessage(msg proto.Message) {
	mStr := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Format(msg)

	var asMap map[string]any
	if err := json.Unmarshal([]byte(mStr), &asMap); err != nil {
		p.Lggr.Errorw("Failed to unmarshal telemetry message for logging",
			"err", err,
			"message_type", msg.ProtoReflect().Descriptor().Name(),
			"json_message", mStr)
		return
	}

	p.Lggr.Infow("[Stellar Monitoring]", "message", asMap, "entity_name", msg.ProtoReflect().Descriptor().Name())
}

var LogAndEmitSuccess = commonmon.LogAndEmitSuccess
var EmitInitiated = commonmon.EmitInitiated
var LogAndEmitError = commonmon.LogAndEmitError
