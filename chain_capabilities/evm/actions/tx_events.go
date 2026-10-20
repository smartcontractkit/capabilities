package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"math/big"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/txevents"
)

// TxEventsOptions wires ChainTransactionObserved emission into the EVM capability. The events
// feed the CRE Transactions Viewer through ChIP and the Blockchain Finality Service.
type TxEventsOptions struct {
	// Emitter publishes the events. Nil or NopEmitter disables emission.
	Emitter txevents.Emitter
	// TransmitterOnly emits only the transactions this node sent. Otherwise every node that
	// observed a transaction emits it and the consumer dedups by (chain selector, tx hash).
	TransmitterOnly bool
	// CapabilityID is stamped on every event.
	CapabilityID string
}

func (o TxEventsOptions) enabled() bool {
	if o.Emitter == nil {
		return false
	}
	_, nop := o.Emitter.(txevents.NopEmitter)
	return !nop
}

// txObservation is what a WriteReport request knows about one transaction.
type txObservation struct {
	hash              evmtypes.Hash
	txStatus          txevents.TxStatus
	receiverStatus    txevents.ReceiverStatus
	gasUsed           uint64
	effectiveGasPrice *big.Int
	fee               *big.Int
	blockNumber       uint64
	errorMessage      string
	// duplicate is true when this node's transaction reverted because another node's
	// transmission of the same report had already landed.
	duplicate bool
}

// merge fills the fields unset in o from in and keeps the duplicate flag once set.
func (o *txObservation) merge(in txObservation) {
	if o.txStatus == txevents.TxStatus_TX_STATUS_UNSPECIFIED {
		o.txStatus = in.txStatus
	}
	if o.receiverStatus == txevents.ReceiverStatus_RECEIVER_STATUS_UNSPECIFIED {
		o.receiverStatus = in.receiverStatus
	}
	if o.gasUsed == 0 {
		o.gasUsed = in.gasUsed
	}
	if o.effectiveGasPrice == nil {
		o.effectiveGasPrice = in.effectiveGasPrice
	}
	if o.fee == nil {
		o.fee = in.fee
	}
	if o.blockNumber == 0 {
		o.blockNumber = in.blockNumber
	}
	if o.errorMessage == "" {
		o.errorMessage = in.errorMessage
	}
	o.duplicate = o.duplicate || in.duplicate
}

// txEventReporter emits exactly one ChainTransactionObserved per transaction a WriteReport
// request saw on chain. Transactions are recorded the moment their hash is known (record,
// ownTransaction). The event carries receipt detail when the receipt is resolved (resolved);
// otherwise it carries the basic information recorded so far and is emitted when the node's own
// transaction turns out to be a duplicate (duplicateOf) or when the request ends (flush).
// Emission never changes the outcome of the write: failures are counted by the emitter and
// logged here. A nil reporter is a no-op.
type txEventReporter struct {
	emitter         txevents.Emitter
	transmitterOnly bool
	capabilityID    string
	chainSelector   uint64
	metadata        capabilities.RequestMetadata
	lggr            logger.SugaredLogger
	now             func() time.Time

	// ownTx is the transaction this node sent in a new transmission attempt, nil otherwise.
	ownTx *evmtypes.TransactionResult
	// known lists the transactions recorded so far, in first-seen order.
	known   []*txObservation
	emitted map[evmtypes.Hash]struct{}
}

func (e *EVM) newTxEventReporter(lggr logger.SugaredLogger, md capabilities.RequestMetadata) *txEventReporter {
	if !e.txEvents.enabled() {
		return nil
	}
	return &txEventReporter{
		emitter:         e.txEvents.Emitter,
		transmitterOnly: e.txEvents.TransmitterOnly,
		capabilityID:    e.txEvents.CapabilityID,
		chainSelector:   e.chainSelector,
		metadata:        md,
		lggr:            lggr,
		now:             time.Now,
		emitted:         map[evmtypes.Hash]struct{}{},
	}
}

func (r *txEventReporter) find(hash evmtypes.Hash) *txObservation {
	for _, obs := range r.known {
		if obs.hash == hash {
			return obs
		}
	}
	return nil
}

// record merges obs into the entry for its hash, creating the entry on first sight. Nothing is
// emitted: the transaction is reported once its receipt is resolved or when the request ends.
func (r *txEventReporter) record(obs txObservation) {
	if r == nil || obs.hash == (evmtypes.Hash{}) {
		return
	}
	if cur := r.find(obs.hash); cur != nil {
		cur.merge(obs)
		return
	}
	c := obs
	r.known = append(r.known, &c)
}

// ownTransaction records the transaction this node just sent with the status the transaction
// manager reported. The manager only returns once the transaction is mined, so this is the
// earliest the hash exists. Fatal results carry no usable hash.
func (r *txEventReporter) ownTransaction(result *evmtypes.TransactionResult) {
	if r == nil || result == nil || result.TxStatus == evmtypes.TxFatal || result.TxHash == (evmtypes.Hash{}) {
		return
	}
	r.ownTx = result
	r.record(txObservation{hash: result.TxHash, txStatus: txStatusFromTxmgr(result.TxStatus)})
}

// resolved emits the transaction with receipt detail, completed with whatever was recorded
// about it before.
func (r *txEventReporter) resolved(ctx context.Context, obs txObservation) {
	if r == nil {
		return
	}
	if cur := r.find(obs.hash); cur != nil {
		obs.merge(*cur)
	}
	r.emit(ctx, txevents.TxDetail_TX_DETAIL_RECEIPT, obs)
}

// duplicateOf emits this node's own transaction as a duplicate when the transmission that landed
// is a different transaction: ours reverted because the report had already been delivered, yet it
// cost gas and must be visible to the user. Its receipt is not fetched, so the detail is basic.
func (r *txEventReporter) duplicateOf(ctx context.Context, landed evmtypes.Hash) {
	if r == nil || r.ownTx == nil || r.ownTx.TxHash == landed {
		return
	}
	r.record(txObservation{hash: r.ownTx.TxHash, duplicate: true})
	r.emit(ctx, txevents.TxDetail_TX_DETAIL_BASIC, *r.find(r.ownTx.TxHash))
}

// flush emits, with basic detail, every recorded transaction not emitted yet. It runs when the
// request ends, so a transaction whose receipt lookup failed, or that was sent by a request that
// failed afterwards, is still reported.
func (r *txEventReporter) flush(ctx context.Context) {
	if r == nil {
		return
	}
	for _, obs := range r.known {
		r.emit(ctx, txevents.TxDetail_TX_DETAIL_BASIC, *obs)
	}
}

func (r *txEventReporter) isOwn(hash evmtypes.Hash) bool {
	return r.ownTx != nil && r.ownTx.TxHash == hash
}

func (r *txEventReporter) emit(ctx context.Context, detail txevents.TxDetail, obs txObservation) {
	if r == nil || obs.hash == (evmtypes.Hash{}) {
		return
	}
	transmitter := r.isOwn(obs.hash)
	if r.transmitterOnly && !transmitter {
		return
	}
	if _, done := r.emitted[obs.hash]; done {
		return
	}
	r.emitted[obs.hash] = struct{}{}

	ev := buildTxEvent(detail, obs, transmitter, r.metadata, r.chainSelector, r.capabilityID, r.now())
	// The request context may already be done (the write failed or timed out); the event must
	// still go out, bounded by the emitter's own timeout.
	if err := r.emitter.Emit(context.WithoutCancel(ctx), ev); err != nil {
		if errors.Is(err, txevents.ErrEmitterUnavailable) {
			return // logged once per process by the emitter
		}
		r.lggr.Errorw("Failed to emit ChainTransactionObserved", append(txevents.LogKVs(ev), "error", err.Error())...)
	}
}

// observeLandedTx records a transaction found through the forwarder's ReportProcessed log, which
// is the earliest point a node that did not send it learns its hash. Such a log can only exist
// for a transaction that succeeded on chain, so the status is known before the receipt is
// fetched; the receiver outcome comes from the forwarder state.
func (e *WriteReport) observeLandedTx(retriever *TxHashRetriever, txHash evmtypes.Hash, receiverStatus evm.ReceiverContractExecutionStatus, errorMessage string) {
	e.txReporter.record(txObservation{
		hash:           txHash,
		txStatus:       txevents.TxStatus_TX_STATUS_SUCCESS,
		receiverStatus: receiverStatusFromReply(receiverStatus),
		blockNumber:    retriever.BlockNumber(txHash),
		errorMessage:   errorMessage,
	})
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

func txStatusFromReceipt(receiptStatus uint64) txevents.TxStatus {
	if receiptStatus == 0 {
		return txevents.TxStatus_TX_STATUS_REVERTED
	}
	return txevents.TxStatus_TX_STATUS_SUCCESS
}

func receiverStatusFromReply(s evm.ReceiverContractExecutionStatus) txevents.ReceiverStatus {
	switch s {
	case evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS:
		return txevents.ReceiverStatus_RECEIVER_STATUS_SUCCESS
	case evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED:
		return txevents.ReceiverStatus_RECEIVER_STATUS_REVERTED
	default:
		return txevents.ReceiverStatus_RECEIVER_STATUS_UNSPECIFIED
	}
}

func blockNumberFromReceipt(bn *big.Int) uint64 {
	if bn == nil || !bn.IsUint64() {
		return 0
	}
	return bn.Uint64()
}

// formatTxHash renders an EVM hash the way explorers and the Transactions Viewer expect.
func formatTxHash(h evmtypes.Hash) string {
	return "0x" + hex.EncodeToString(h[:])
}

func bigIntString(v *big.Int) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func buildTxEvent(detail txevents.TxDetail, obs txObservation, transmitter bool, md capabilities.RequestMetadata, chainSelector uint64, capabilityID string, at time.Time) *txevents.ChainTransactionObserved {
	return &txevents.ChainTransactionObserved{
		WorkflowExecutionId: md.WorkflowExecutionID,
		WorkflowId:          md.WorkflowID,
		WorkflowOwner:       md.WorkflowOwner,
		ReferenceId:         md.ReferenceID,
		WorkflowDonId:       md.WorkflowDonID,
		OrgId:               md.OrgID,

		ChainSelector: chainSelector,
		TxHash:        formatTxHash(obs.hash),
		CapabilityId:  capabilityID,

		Detail:            detail,
		TxStatus:          obs.txStatus,
		ReceiverStatus:    obs.receiverStatus,
		TransactionFee:    bigIntString(obs.fee),
		GasUsed:           obs.gasUsed,
		EffectiveGasPrice: bigIntString(obs.effectiveGasPrice),
		BlockNumber:       obs.blockNumber,
		ErrorMessage:      obs.errorMessage,

		ObservedAtMs: at.UnixMilli(),
		Transmitter:  transmitter,
		Duplicate:    obs.duplicate,
	}
}
