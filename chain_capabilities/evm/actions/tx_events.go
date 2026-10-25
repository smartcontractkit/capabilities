package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/txevents"
)

// txEventsEnabled reports whether emitter actually publishes events.
func txEventsEnabled(emitter txevents.Emitter) bool {
	if emitter == nil {
		return false
	}
	_, nop := emitter.(txevents.NopEmitter)
	return !nop
}

// txEventReporter emits one ChainTransactionObserved for every transaction a WriteReport request
// learns was mined, at the moment it learns it: the node's own transaction when the transaction
// manager returns (it only returns once the transaction is mined), and transactions sent by
// other nodes when the forwarder's ReportProcessed log reveals their hash. Each hash is emitted
// once per request; every node emits what it saw and consumers dedup by txevents.Key. Emission
// never changes the outcome of the write: failures are counted by the emitter and logged here.
// A nil reporter is a no-op.
type txEventReporter struct {
	emitter       txevents.Emitter
	chainSelector uint64
	metadata      capabilities.RequestMetadata
	lggr          logger.SugaredLogger
	now           func() time.Time

	emitted map[evmtypes.Hash]struct{}
}

func (e *EVM) newTxEventReporter(lggr logger.SugaredLogger, md capabilities.RequestMetadata) *txEventReporter {
	if !txEventsEnabled(e.txEvents) {
		return nil
	}
	return &txEventReporter{
		emitter:       e.txEvents,
		chainSelector: e.chainSelector,
		metadata:      md,
		lggr:          lggr,
		now:           time.Now,
		emitted:       map[evmtypes.Hash]struct{}{},
	}
}

// sent reports the transaction this node just sent, with the status the transaction manager
// returned. Fatal results carry no usable hash and are skipped.
func (r *txEventReporter) sent(ctx context.Context, result *evmtypes.TransactionResult) {
	if r == nil || result == nil || result.TxStatus == evmtypes.TxFatal {
		return
	}
	r.observed(ctx, result.TxHash, txStatusFromTxmgr(result.TxStatus))
}

// landed reports a transaction found through the forwarder's ReportProcessed log. Such a log
// only exists for a transaction that succeeded on chain, so its status is known without the
// receipt.
func (r *txEventReporter) landed(ctx context.Context, hash evmtypes.Hash) {
	r.observed(ctx, hash, txevents.TxStatus_TX_STATUS_SUCCESS)
}

// observed emits the event for hash unless this request already did.
func (r *txEventReporter) observed(ctx context.Context, hash evmtypes.Hash, status txevents.TxStatus) {
	if r == nil || hash == (evmtypes.Hash{}) {
		return
	}
	if _, done := r.emitted[hash]; done {
		return
	}
	r.emitted[hash] = struct{}{}

	ev := newTxEvent(hash, status, r.metadata, r.chainSelector, r.now())
	// The request context may already be done (the write failed or timed out); the event must
	// still go out, bounded by the emitter's own timeout.
	if err := r.emitter.Emit(context.WithoutCancel(ctx), ev); err != nil {
		if errors.Is(err, txevents.ErrEmitterUnavailable) {
			return // logged once per process by the emitter
		}
		r.lggr.Errorw("Failed to emit ChainTransactionObserved", append(txevents.LogKVs(ev), "error", err.Error())...)
	}
}

func txStatusFromTxmgr(s evmtypes.TransactionStatus) txevents.TxStatus {
	switch s {
	case evmtypes.TxSuccess:
		return txevents.TxStatus_TX_STATUS_SUCCESS
	case evmtypes.TxReverted:
		return txevents.TxStatus_TX_STATUS_REVERTED
	default:
		return txevents.TxStatus_TX_STATUS_UNSPECIFIED
	}
}

// formatTxHash renders an EVM hash the way explorers and the Transactions Viewer expect.
func formatTxHash(h evmtypes.Hash) string {
	return "0x" + hex.EncodeToString(h[:])
}

func newTxEvent(hash evmtypes.Hash, status txevents.TxStatus, md capabilities.RequestMetadata, chainSelector uint64, at time.Time) *txevents.ChainTransactionObserved {
	return &txevents.ChainTransactionObserved{
		WorkflowExecutionId: md.WorkflowExecutionID,
		WorkflowId:          md.WorkflowID,
		ChainSelector:       chainSelector,
		TxHash:              formatTxHash(hash),
		TxStatus:            status,
		ObservedAtMs:        at.UnixMilli(),
	}
}
