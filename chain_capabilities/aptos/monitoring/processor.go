package monitoring

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

// processor dispatches telemetry messages to metrics and logs for Aptos operations.
type processor struct {
	metrics Metrics
	lggr    logger.Logger
}

func NewProcessor(lggr logger.Logger, metrics Metrics) (beholder.ProtoProcessor, error) {
	return &processor{
		lggr:    lggr,
		metrics: metrics,
	}, nil
}

func (p *processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	switch msg := m.(type) {
	case *ViewInitiated, *WriteReportInitiated:
		p.logMessage(m)
	case *ViewSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnViewSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish ViewSuccess metrics: %w", err)
		}
	case *ViewError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnViewError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish ViewError metrics: %w", err)
			}
		}
	case *WriteReportSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccess metrics: %w", err)
		}
	case *WriteReportError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnWriteReportError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish WriteReportError metrics: %w", err)
			}
		}
	case *WriteReportTxFeeCalculationError:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportTxFeeCalculationError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTxFeeCalculationError metrics: %w", err)
		}
	case *WriteReportDuplicateTx:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportDuplicateTx(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportDuplicateTx metrics: %w", err)
		}
	case *WriteReportSuccessfulEarlyReturn:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportSuccessfulEarlyReturn(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccessfulEarlyReturn metrics: %w", err)
		}
	case *WriteReportTransmitterMismatch:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportTransmitterMismatch(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTransmitterMismatch metrics: %w", err)
		}
	case *WriteReportP2PConfigIncomplete:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportP2PConfigIncomplete(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportP2PConfigIncomplete metrics: %w", err)
		}
	default:
		return nil
	}
	return nil
}

func (p *processor) logMessage(msg proto.Message) {
	mStr := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Format(msg)

	var asMap map[string]any
	if err := json.Unmarshal([]byte(mStr), &asMap); err != nil {
		p.lggr.Errorw("Failed to unmarshal telemetry message for logging",
			"err", err,
			"message_type", msg.ProtoReflect().Descriptor().Name(),
			"json_message", mStr)
		return
	}

	p.lggr.Infow("[Aptos Monitoring]", "message", asMap, "entity_name", msg.ProtoReflect().Descriptor().Name())
}

var LogAndEmitSuccess = commonmon.LogAndEmitSuccess
var EmitInitiated = commonmon.EmitInitiated
var LogAndEmitError = commonmon.LogAndEmitError
