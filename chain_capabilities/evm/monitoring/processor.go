package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
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
		p.logMessage(m)
	// -- CallContract --
	case *CallContractSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnCallContractSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish CallContractSuccess metrics: %w", err)
		}
	case *CallContractError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnCallContractError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish CallContractError metrics: %w", err)
			}
		}
	// -- WriteReport --
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
	case *WriteReportInvalidTransmissionState:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportInvalidTransmissionState(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportInvalidTransmissionState metrics: %w", err)
		}
	case *WriteReportDuplicateTx:
		p.logMessage(msg)
		if err := p.metrics.OnWriteReportDuplicateTx(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish WriteReportDuplicateTx metrics: %w", err)
		}
	// -- LogTrigger --
	case *LogTriggerSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnLogTriggerSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerSuccess metrics: %w", err)
		}
	case *LogTriggerError:
		p.logMessage(msg)
		if err := p.metrics.OnLogTriggerError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerError metrics: %w", err)
		}
	case *LogTriggerCleanUpError:
		p.logMessage(msg)
		if err := p.metrics.OnLogTriggerCleanUpError(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish LogTriggerCleanUpError metrics: %w", err)
		}
	case *LogTriggerEventDroppedError:
		p.logMessage(msg)
		if !msg.GetIsLimitError() {
			if err := p.metrics.OnTriggerEventDroppedError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish TriggerEventDroppedError metrics: %w", err)
			}
		}
	// -- FilterLogs --
	case *FilterLogsSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnFilterLogsSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish FilterLogsSuccess metrics: %w", err)
		}
	case *FilterLogsError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnFilterLogsError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish FilterLogsError metrics: %w", err)
			}
		}
	// --- BalanceAt ---
	case *BalanceAtSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnBalanceAtSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish BalanceAtSuccess metrics: %w", err)
		}
	case *BalanceAtError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnBalanceAtError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish BalanceAtError metrics: %w", err)
			}
		}
	// -- EstimateGas --
	case *EstimateGasSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnEstimateGasSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish EstimateGasSuccess metrics: %w", err)
		}
	case *EstimateGasError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnEstimateGasError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish EstimateGasError metrics: %w", err)
			}
		}
	// -- GetTransactionByHash --
	case *GetTransactionByHashSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnGetTransactionByHashSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionByHashSuccess metrics: %w", err)
		}
	case *GetTransactionByHashError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnGetTransactionByHashError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish GetTransactionByHashError metrics: %w", err)
			}
		}
	// -- GetTransactionReceipt --
	case *GetTransactionReceiptSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnGetTransactionReceiptSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish GetTransactionReceiptSuccess metrics: %w", err)
		}
	case *GetTransactionReceiptError:
		p.logMessage(msg)
		if !msg.GetIsUserError() {
			if err := p.metrics.OnGetTransactionReceiptError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish GetTransactionReceiptError metrics: %w", err)
			}
		}
	// -- HeaderByNumber --
	case *HeaderByNumberSuccess:
		p.logMessage(msg)
		if err := p.metrics.OnHeaderByNumberSuccess(ctx, msg); err != nil {
			return fmt.Errorf("failed to publish HeaderByNumberSuccess metrics: %w", err)
		}
	case *HeaderByNumberError:
		if !msg.GetIsUserError() {
			p.logMessage(msg)
			if err := p.metrics.OnHeaderByNumberError(ctx, msg); err != nil {
				return fmt.Errorf("failed to publish HeaderByNumberError metrics: %w", err)
			}
		}
	default:
		// Unknown message types are silently ignored (noop)
		return nil
	}
	return nil
}

func (p *processor) logMessage(msg proto.Message) {
	mStr := protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}.Format(msg)

	// Convert to map for structured logging
	var asMap map[string]any
	err := json.Unmarshal([]byte(mStr), &asMap)
	if err != nil {
		p.lggr.Errorw("Failed to unmarshal telemetry message for logging",
			"err", err,
			"message_type", msg.ProtoReflect().Descriptor().Name(),
			"json_message", mStr)
		return
	}

	p.lggr.Infow("[EVM Monitoring]", "message", asMap, "entity_name", msg.ProtoReflect().Descriptor().Name())
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
