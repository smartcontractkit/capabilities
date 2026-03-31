package monitoring

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

// processor dispatches telemetry messages to metrics and logs for Aptos operations.
type processor struct {
	metrics Metrics
	emitter beholder.ProtoEmitter
}

func NewProcessor(emitter beholder.ProtoEmitter, metrics Metrics) (beholder.ProtoProcessor, error) {
	return &processor{
		emitter: emitter,
		metrics: metrics,
	}, nil
}

func (p *processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	switch msg := m.(type) {
	case *WriteReportInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportInitiated log: %w", err)
		}
	case *WriteReportSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportSuccess log: %w", err)
		}
		if err := p.metrics.OnWriteReportSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccess metrics: %w", err)
		}
	case *WriteReportError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnWriteReportError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish WriteReportError metrics: %w", err)
			}
		}
	case *WriteReportTxFeeCalculationError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportTxFeeCalculationError log: %w", err)
		}
		if err := p.metrics.OnWriteReportTxFeeCalculationError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTxFeeCalculationError metrics: %w", err)
		}
	case *WriteReportDuplicateTx:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportDuplicateTx log: %w", err)
		}
		if err := p.metrics.OnWriteReportDuplicateTx(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportDuplicateTx metrics: %w", err)
		}
	case *WriteReportSuccessfulEarlyReturn:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportSuccessfulEarlyReturn log: %w", err)
		}
		if err := p.metrics.OnWriteReportSuccessfulEarlyReturn(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccessfulEarlyReturn metrics: %w", err)
		}
	case *WriteReportTransmitterMismatch:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportTransmitterMismatch log: %w", err)
		}
		if err := p.metrics.OnWriteReportTransmitterMismatch(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTransmitterMismatch metrics: %w", err)
		}
	default:
		return nil
	}
	return nil
}

var LogAndEmitSuccess = commonmon.LogAndEmitSuccess
var EmitInitiated = commonmon.EmitInitiated
var LogAndEmitError = commonmon.LogAndEmitError
