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
	// exclude summary to avoid duplicating potentially large error msg when logged locally
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

// attrsToErrorKV converts a slice of KeyValue into a flat []any of alternating key/value for lggr kvs.
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
