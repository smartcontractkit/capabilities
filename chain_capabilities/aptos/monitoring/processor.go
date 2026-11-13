package monitoring

import (
	"context"
	"fmt"

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
	case *ViewSuccess:
		if err := p.metrics.OnViewSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish ViewSuccess metrics: %w", err)
		}
	case *ViewError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnViewError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish ViewError metrics: %w", err)
			}
		}
	case *WriteReportSuccess:
		if err := p.metrics.OnWriteReportSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccess metrics: %w", err)
		}
	case *WriteReportError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnWriteReportError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish WriteReportError metrics: %w", err)
			}
		}
	case *WriteReportTxFeeCalculationError:
		if err := p.metrics.OnWriteReportTxFeeCalculationError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTxFeeCalculationError metrics: %w", err)
		}
	case *WriteReportDuplicateTx:
		if err := p.metrics.OnWriteReportDuplicateTx(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportDuplicateTx metrics: %w", err)
		}
	case *WriteReportSuccessfulEarlyReturn:
		if err := p.metrics.OnWriteReportSuccessfulEarlyReturn(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccessfulEarlyReturn metrics: %w", err)
		}
	case *WriteReportTransmitterMismatch:
		if err := p.metrics.OnWriteReportTransmitterMismatch(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTransmitterMismatch metrics: %w", err)
		}
	case *WriteReportP2PConfigIncomplete:
		if err := p.metrics.OnWriteReportP2PConfigIncomplete(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportP2PConfigIncomplete metrics: %w", err)
		}
	case *WriteReportTxInfoRetrievalPhase:
		if err := p.metrics.OnWriteReportTxInfoRetrievalPhase(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTxInfoRetrievalPhase metrics: %w", err)
		}
	case *WriteReportInvokeOnReportDuration:
		if err := p.metrics.OnWriteReportInvokeOnReportDuration(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportInvokeOnReportDuration metrics: %w", err)
		}
	default:
		return nil
	}
	return nil
}

var LogAndEmitSuccess = commonmon.LogAndEmitSuccess
var EmitInitiated = commonmon.EmitInitiated
var LogAndEmitError = commonmon.LogAndEmitError
