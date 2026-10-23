package monitoring

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	commonmon "github.com/smartcontractkit/capabilities/chain_capabilities/common/monitoring"
)

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

// Process dispatches telemetry messages to metrics and logs for all EVM operations
func (p *processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	// Process metrics for known message types
	switch msg := m.(type) {
	// -- Initiated messages --
	case *CallContractInitiated, *WriteReportInitiated, *LogTriggerInitiated,
		*FilterLogsInitiated, *BalanceAtInitiated, *EstimateGasInitiated,
		*GetTransactionByHashInitiated, *GetTransactionReceiptInitiated, *HeaderByNumberInitiated:
	// -- CallContract --
	case *CallContractSuccess:
		if err := p.metrics.OnCallContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish CallContractSuccess metrics: %w", err)
		}
	case *CallContractError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnCallContractError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish CallContractError metrics: %w", err)
			}
		}
	// -- WriteReport --
	case *WriteReportSuccess:
		if err := p.metrics.OnWriteReportSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccess metrics: %w", err)
		}
	case *WriteReportSuccessfulEarlyReturn:
		if err := p.metrics.OnWriteReportSuccessfulEarlyReturn(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportSuccessfulEarlyReturn metrics: %w", err)
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
	case *WriteReportInvalidTransmissionState:
		if err := p.metrics.OnWriteReportInvalidTransmissionState(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportInvalidTransmissionState metrics: %w", err)
		}
	case *WriteReportDuplicateTx:
		if err := p.metrics.OnWriteReportDuplicateTx(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportDuplicateTx metrics: %w", err)
		}
	case *WriteReportInsufficientGasRetry:
		if err := p.metrics.OnWriteReportInsufficientGasRetry(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportInsufficientGasRetry metrics: %w", err)
		}
	// -- LogTrigger --
	case *LogTriggerSuccess:
		if err := p.metrics.OnLogTriggerSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerSuccess metrics: %w", err)
		}
	case *LogTriggerError:
		if err := p.metrics.OnLogTriggerError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerError metrics: %w", err)
		}
	case *LogTriggerCleanUpError:
		if err := p.metrics.OnLogTriggerCleanUpError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerCleanUpError metrics: %w", err)
		}
	case *LogTriggerEventDroppedError:
		if !msg.GetIsLimitError() {
			if err := p.metrics.OnTriggerEventDroppedError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish TriggerEventDroppedError metrics: %w", err)
			}
		}
	// -- FilterLogs --
	case *FilterLogsSuccess:
		if err := p.metrics.OnFilterLogsSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish FilterLogsSuccess metrics: %w", err)
		}
	case *FilterLogsError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnFilterLogsError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish FilterLogsError metrics: %w", err)
			}
		}
	// --- BalanceAt ---
	case *BalanceAtSuccess:
		if err := p.metrics.OnBalanceAtSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish BalanceAtSuccess metrics: %w", err)
		}
	case *BalanceAtError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnBalanceAtError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish BalanceAtError metrics: %w", err)
			}
		}
	// -- EstimateGas --
	case *EstimateGasSuccess:
		if err := p.metrics.OnEstimateGasSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish EstimateGasSuccess metrics: %w", err)
		}
	case *EstimateGasError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnEstimateGasError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish EstimateGasError metrics: %w", err)
			}
		}
	// -- GetTransactionByHash --
	case *GetTransactionByHashSuccess:
		if err := p.metrics.OnGetTransactionByHashSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionByHashSuccess metrics: %w", err)
		}
	case *GetTransactionByHashError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnGetTransactionByHashError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish GetTransactionByHashError metrics: %w", err)
			}
		}
	// -- GetTransactionReceipt --
	case *GetTransactionReceiptSuccess:
		if err := p.metrics.OnGetTransactionReceiptSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionReceiptSuccess metrics: %w", err)
		}
	case *GetTransactionReceiptError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnGetTransactionReceiptError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish GetTransactionReceiptError metrics: %w", err)
			}
		}
	// -- HeaderByNumber --
	case *HeaderByNumberSuccess:
		if err := p.metrics.OnHeaderByNumberSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish HeaderByNumberSuccess metrics: %w", err)
		}
	case *HeaderByNumberError:
		if !msg.GetIsUserError() {
			if err := p.metrics.OnHeaderByNumberError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish HeaderByNumberError metrics: %w", err)
			}
		}
	// -- TransmissionScheduler --
	case *TransmissionSchedulerNodeNotFoundInDon:
		if err := p.metrics.OnTransmissionSchedulerNodeNotFoundInDon(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish TransmissionSchedulerNodeNotFoundInDon metrics: %w", err)
		}
	default:
		// Unknown message types are silently ignored (noop)
		return nil
	}
	return nil
}

var LogAndEmitSuccess = commonmon.LogAndEmitSuccess
var EmitInitiated = commonmon.EmitInitiated
var LogAndEmitError = commonmon.LogAndEmitError
