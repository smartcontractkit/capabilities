package txevents

import (
	"context"
	"errors"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

// Reporter emits at most one ChainTransactionObserved per write, for the transaction whose
// fee the capability bills. A nil *Reporter is a no-op, so callers enable it only when
// transaction events are turned on.
type Reporter struct {
	emitter       Emitter
	chainSelector uint64
	metadata      capabilities.RequestMetadata
	lggr          logger.SugaredLogger
	now           func() time.Time

	emitted bool
}

// Enabled reports whether emitter actually publishes events.
func Enabled(emitter Emitter) bool {
	if emitter == nil {
		return false
	}
	_, nop := emitter.(NopEmitter)
	return !nop
}

// NewReporter returns a reporter for one write, or nil when emission is disabled.
func NewReporter(emitter Emitter, chainSelector uint64, md capabilities.RequestMetadata, lggr logger.Logger) *Reporter {
	if !Enabled(emitter) {
		return nil
	}
	return &Reporter{
		emitter:       emitter,
		chainSelector: chainSelector,
		metadata:      md,
		lggr:          logger.Sugared(lggr),
		now:           time.Now,
	}
}

// Charged emits the event for the transaction the write charges for. Later calls in the same
// write are ignored, and an empty hash is skipped. Emission never changes the write outcome.
func (r *Reporter) Charged(ctx context.Context, txHash string, status TxStatus) {
	if r == nil || r.emitted || txHash == "" {
		return
	}
	r.emitted = true

	ev := &ChainTransactionObserved{
		WorkflowExecutionId: r.metadata.WorkflowExecutionID,
		WorkflowId:          r.metadata.WorkflowID,
		ChainSelector:       r.chainSelector,
		TxHash:              txHash,
		TxStatus:            status,
		ObservedAtMs:        r.now().UnixMilli(),
	}
	// The request context may already be done (the write failed or timed out); the event must
	// still go out, bounded by the emitter's own timeout.
	if err := r.emitter.Emit(context.WithoutCancel(ctx), ev); err != nil {
		if errors.Is(err, ErrEmitterUnavailable) {
			return // logged once per process by the emitter
		}
		r.lggr.Errorw("Failed to emit ChainTransactionObserved", append(LogKVs(ev), "error", err.Error())...)
	}
}
