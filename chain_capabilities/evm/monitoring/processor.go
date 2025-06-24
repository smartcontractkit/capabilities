package monitoring

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
)

type processor struct {
	metrics *Metrics
	emitter beholder.ProtoEmitter
}

func NewProcessor(emitter beholder.ProtoEmitter) (beholder.ProtoProcessor, error) {
	metrics, err := NewMetrics()
	if err != nil {
		return nil, fmt.Errorf("failed to create metrics: %w", err)
	}
	return &processor{
		emitter: emitter,
		metrics: metrics,
	}, nil
}

// Process dispatches telemetry messages to metrics and logs for all EVM operations
func (p *processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	switch msg := m.(type) {
	// -- CallContract --
	case *CallContractInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractInitiated log: %w", err)
		}
	case *CallContractSuccess:
		if err := p.metrics.OnCallContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish CallContractSuccess metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractSuccess log: %w", err)
		}
	case *CallContractError:
		if err := p.metrics.OnCallContractError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish CallContractError metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractError log: %w", err)
		}
	// -- FilterLogs --
	case *FilterLogsInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit FilterLogsInitiated log: %w", err)
		}
	case *FilterLogsSuccess:
		if err := p.metrics.OnFilterLogsSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish FilterLogsSuccess metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit FilterLogsSuccess log: %w", err)
		}
	case *FilterLogsError:
		if err := p.metrics.OnFilterLogsError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish FilterLogsError metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit FilterLogsError log: %w", err)
		}
	// --- BalanceAt ---
	case *BalanceAtInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit BalanceAtInitiated log: %w", err)
		}
	case *BalanceAtSuccess:
		if err := p.metrics.OnBalanceAtSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish BalanceAtSuccess metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit BalanceAtSuccess log: %w", err)
		}
	case *BalanceAtError:
		if err := p.metrics.OnBalanceAtError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish BalanceAtError metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit BalanceAtError log: %w", err)
		}
	// -- EstimateGas --
	case *EstimateGasInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit EstimateGasInitiated log: %w", err)
		}
	case *EstimateGasSuccess:
		if err := p.metrics.OnEstimateGasSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish EstimateGasSuccess metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit EstimateGasSuccess log: %w", err)
		}
	case *EstimateGasError:
		if err := p.metrics.OnEstimateGasError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish EstimateGasError metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit EstimateGasError log: %w", err)
		}
	// -- GetTransactionByHash --
	case *GetTransactionByHashInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionByHashInitiated log: %w", err)
		}
	case *GetTransactionByHashSuccess:
		if err := p.metrics.OnGetTransactionByHashSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionByHashSuccess metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionByHashSuccess log: %w", err)
		}
	case *GetTransactionByHashError:
		if err := p.metrics.OnGetTransactionByHashError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionByHashError metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionByHashError log: %w", err)
		}
	// -- GetTransactionReceipt --
	case *GetTransactionReceiptInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionReceiptInitiated log: %w", err)
		}
	case *GetTransactionReceiptSuccess:
		if err := p.metrics.OnGetTransactionReceiptSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionReceiptSuccess metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionReceiptSuccess log: %w", err)
		}
	case *GetTransactionReceiptError:
		if err := p.metrics.OnGetTransactionReceiptError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionReceiptError metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionReceiptError log: %w", err)
		}
	// -- LatestAndFinalizedHead --
	case *LatestAndFinalizedHeadInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit LatestAndFinalizedHeadInitiated log: %w", err)
		}
	case *LatestAndFinalizedHeadSuccess:
		if err := p.metrics.OnLatestAndFinalizedHeadSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LatestAndFinalizedHeadSuccess metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit LatestAndFinalizedHeadSuccess log: %w", err)
		}
	case *LatestAndFinalizedHeadError:
		if err := p.metrics.OnLatestAndFinalizedHeadError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LatestAndFinalizedHeadError metrics: %w", err)
		}
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit LatestAndFinalizedHeadError log: %w", err)
		}
	default:
		return nil
	}
	return nil
}
