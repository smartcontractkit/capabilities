package monitoring

import (
	"context"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

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

// Process dispatches telemetry messages to metrics and logs for all EVM operations
func (p *processor) Process(ctx context.Context, m proto.Message, attrKVs ...any) error {
	switch msg := m.(type) {
	// -- CallContract --
	case *CallContractInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractInitiated log: %w", err)
		}
	case *CallContractSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractSuccess log: %w", err)
		}
		if err := p.metrics.OnCallContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish CallContractSuccess metrics: %w", err)
		}
	case *CallContractError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit CallContractError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnCallContractError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish CallContractError metrics: %w", err)
			}
		}
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
	case *WriteReportTxFeeCalculationError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportTxFeeCalculationError log: %w", err)
		}
		if err := p.metrics.OnWriteReportTxFeeCalculationError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportTxFeeCalculationError metrics: %w", err)
		}
	case *WriteReportInvalidTransmissionState:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportInvalidTransmissionState log: %w", err)
		}
		if err := p.metrics.OnWriteReportInvalidTransmissionState(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportInvalidTransmissionState metrics: %w", err)
		}
	case *WriteReportDuplicateTx:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit WriteReportDuplicateTx log: %w", err)
		}
		if err := p.metrics.OnWriteReportDuplicateTx(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportDuplicateTx metrics: %w", err)
		}
	// -- LogTrigger --
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
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit FilterLogsError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnFilterLogsError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish FilterLogsError metrics: %w", err)
			}
		}
	// --- BalanceAt ---
	case *BalanceAtInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit BalanceAtInitiated log: %w", err)
		}
	case *BalanceAtSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit BalanceAtSuccess log: %w", err)
		}
		if err := p.metrics.OnBalanceAtSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish BalanceAtSuccess metrics: %w", err)
		}
	case *BalanceAtError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit BalanceAtError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnBalanceAtError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish BalanceAtError metrics: %w", err)
			}
		}
	// -- EstimateGas --
	case *EstimateGasInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit EstimateGasInitiated log: %w", err)
		}
	case *EstimateGasSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit EstimateGasSuccess log: %w", err)
		}
		if err := p.metrics.OnEstimateGasSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish EstimateGasSuccess metrics: %w", err)
		}
	case *EstimateGasError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit EstimateGasError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnEstimateGasError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish EstimateGasError metrics: %w", err)
			}
		}
	// -- GetTransactionByHash --
	case *GetTransactionByHashInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionByHashInitiated log: %w", err)
		}
	case *GetTransactionByHashSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionByHashSuccess log: %w", err)
		}
		if err := p.metrics.OnGetTransactionByHashSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionByHashSuccess metrics: %w", err)
		}
	case *GetTransactionByHashError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionByHashError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnGetTransactionByHashError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish GetTransactionByHashError metrics: %w", err)
			}
		}
	// -- GetTransactionReceipt --
	case *GetTransactionReceiptInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionReceiptInitiated log: %w", err)
		}
	case *GetTransactionReceiptSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionReceiptSuccess log: %w", err)
		}
		if err := p.metrics.OnGetTransactionReceiptSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionReceiptSuccess metrics: %w", err)
		}
	case *GetTransactionReceiptError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit GetTransactionReceiptError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnGetTransactionReceiptError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish GetTransactionReceiptError metrics: %w", err)
			}
		}
	// -- LatestAndFinalizedHead --
	case *HeaderByNumberInitiated:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit HeaderByNumberInitiated log: %w", err)
		}
	case *HeaderByNumberSuccess:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit HeaderByNumberSuccess log: %w", err)
		}
		if err := p.metrics.OnHeaderByNumberSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish HeaderByNumberSuccess metrics: %w", err)
		}
	case *HeaderByNumberError:
		if err := p.emitter.EmitWithLog(ctx, msg, attrKVs...); err != nil {
			return fmt.Errorf("failed to emit HeaderByNumberError log: %w", err)
		}
		if !msg.GetIsUserError() {
			if err := p.metrics.OnHeaderByNumberError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish HeaderByNumberError metrics: %w", err)
			}
		}
	default:
		return nil
	}
	return nil
}

func LogAndEmitSuccess(
	ctx context.Context,
	successMessage string,
	lggr logger.Logger,
	beholderProcessor beholder.ProtoProcessor,
	m Message,
) {
	lggr.Infow(successMessage, attrsToErrorKV(m.LogAttributes())...)
	if err := beholderProcessor.Process(ctx, m); err != nil {
		lggr.Errorw(fmt.Sprintf("Failed to process %s message", getMessageName(m)), "err", err)
	}
}

func EmitInitiated(
	ctx context.Context,
	lggr logger.Logger,
	beholderProcessor beholder.ProtoProcessor,
	m proto.Message,
) {
	if err := beholderProcessor.Process(ctx, m); err != nil {
		lggr.Errorw(fmt.Sprintf("Failed to process %s message", getMessageName(m)), "err", err)
	}
}

func LogAndEmitError(
	ctx context.Context,
	lggr logger.Logger,
	beholderProcessor beholder.ProtoProcessor,
	eM ErrorMessage,
) {
	// exclude summary to avoid duplicating potentially large error msg, when logged localy
	localLogAttributes := eM.LogAttributes()
	for i := 0; i < len(localLogAttributes); i++ {
		if localLogAttributes[i].Key == "summary" {
			localLogAttributes = append(localLogAttributes[:i], localLogAttributes[i+1:]...)
			break
		}
	}

	lggr.Errorw(eM.GetSummary()+" err: "+eM.GetCause(), attrsToErrorKV(localLogAttributes)...)
	if err := beholderProcessor.Process(ctx, eM); err != nil {
		lggr.Errorw(fmt.Sprintf("Failed to process %s message", getMessageName(eM)), "err", err)
	}
}

// attrsToErrorKV converts a slice of KeyValue into
// a flat []interface{} of alternating key and value for loggr kvs.
func attrsToErrorKV(attrs []attribute.KeyValue) []any {
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

func getMessageName(r proto.Message) string {
	fullNameSplit := strings.Split(beholder.ToSchemaFullName(r), ".")
	return fullNameSplit[len(fullNameSplit)-1]
}
