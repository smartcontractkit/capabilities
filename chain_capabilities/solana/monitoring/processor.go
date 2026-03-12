package monitoring

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

// processor dispatches telemetry messages to metrics and logs for Solana operations.
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
	// -- WriteReport --
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
	case *LogTriggerInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit LogTriggerInitiated log: %w", err)
		}
	case *LogTriggerSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit LogTriggerSuccess log: %w", err)
		}
		if err := p.metrics.OnLogTriggerSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerSuccess metrics: %w", err)
		}
	case *LogTriggerError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit LogTriggerError log: %w", err)
		}
		if err := p.metrics.OnLogTriggerError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerError metrics: %w", err)
		}
	case *LogTriggerCleanUpError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit LogTriggerCleanUpError log: %w", err)
		}
		if err := p.metrics.OnLogTriggerCleanUpError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerCleanUpError metrics: %w", err)
		}
	case *LogTriggerEventDroppedError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit TriggerEventDroppedError log: %w", err)
		}
		if !msg.GetIsLimitError() {
			if err := p.metrics.OnTriggerEventDroppedError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish TriggerEventDroppedError metrics: %w", err)
			}
		}
	default:
		return nil
	}
	return nil
}

var LogAndEmitSuccess = commonmon.LogAndEmitSuccess
var EmitInitiated = commonmon.EmitInitiated
var LogAndEmitError = commonmon.LogAndEmitError
