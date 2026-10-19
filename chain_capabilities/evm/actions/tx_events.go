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

// txObservation is one transaction a WriteReport request saw on chain.
type txObservation struct {
	hash           evmtypes.Hash
	txStatus       txevents.TxStatus
	receiverStatus txevents.ReceiverStatus
	fee            *big.Int
	blockNumber    uint64
	errorMessage   string
	// transmitter is true when this node sent the transaction.
	transmitter bool
	// duplicate is true when this node's transaction reverted because another node's
	// transmission of the same report had already landed.
	duplicate bool
}

// txObservations accumulates the observations of one request, one entry per transaction hash,
// in the order the hashes were first seen.
type txObservations struct {
	list []*txObservation
}

func (o *txObservations) find(hash evmtypes.Hash) *txObservation {
	for _, obs := range o.list {
		if obs.hash == hash {
			return obs
		}
	}
	return nil
}

// record merges obs into the entry for its hash: unset fields are filled in and flags are OR'd,
// so a hash-only observation taken before the receipt lookup is enriched by a later one.
func (o *txObservations) record(obs txObservation) {
	cur := o.find(obs.hash)
	if cur == nil {
		c := obs
		o.list = append(o.list, &c)
		return
	}
	if cur.txStatus == txevents.TxStatus_TX_STATUS_UNSPECIFIED {
		cur.txStatus = obs.txStatus
	}
	if cur.receiverStatus == txevents.ReceiverStatus_RECEIVER_STATUS_UNSPECIFIED {
		cur.receiverStatus = obs.receiverStatus
	}
	if cur.fee == nil {
		cur.fee = obs.fee
	}
	if cur.blockNumber == 0 {
		cur.blockNumber = obs.blockNumber
	}
	if cur.errorMessage == "" {
		cur.errorMessage = obs.errorMessage
	}
	cur.transmitter = cur.transmitter || obs.transmitter
	cur.duplicate = cur.duplicate || obs.duplicate
}

// recordOwnTx records the transaction this node sent in a new transmission attempt. When the
// reply was built from a different transaction, ours reverted as a duplicate of a transmission
// that had already landed; it still cost gas and is recorded as such. When no reply transaction
// is known (the request failed after sending), the transaction is recorded with the status the
// transaction manager reported and no duplicate flag.
func (o *txObservations) recordOwnTx(result *evmtypes.TransactionResult) {
	if result == nil || result.TxStatus == evmtypes.TxFatal || result.TxHash == (evmtypes.Hash{}) {
		return
	}
	obs := txObservation{
		hash:        result.TxHash,
		txStatus:    txStatusFromTxmgr(result.TxStatus),
		transmitter: true,
		duplicate:   len(o.list) > 0 && o.find(result.TxHash) == nil,
	}
	o.record(obs)
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

func buildTxEvent(obs *txObservation, md capabilities.RequestMetadata, chainSelector uint64, capabilityID string, observedAt time.Time) *txevents.ChainTransactionObserved {
	ev := &txevents.ChainTransactionObserved{
		WorkflowExecutionId: md.WorkflowExecutionID,
		WorkflowId:          md.WorkflowID,
		WorkflowOwner:       md.WorkflowOwner,
		ReferenceId:         md.ReferenceID,
		WorkflowDonId:       md.WorkflowDonID,
		OrgId:               md.OrgID,

		ChainSelector: chainSelector,
		TxHash:        formatTxHash(obs.hash),
		CapabilityId:  capabilityID,

		TxStatus:       obs.txStatus,
		ReceiverStatus: obs.receiverStatus,
		BlockNumber:    obs.blockNumber,
		ErrorMessage:   obs.errorMessage,

		ObservedAtMs: observedAt.UnixMilli(),
		Transmitter:  obs.transmitter,
		Duplicate:    obs.duplicate,
	}
	if obs.fee != nil {
		ev.TransactionFee = obs.fee.String()
	}
	return ev
}

// emitTxEvents publishes one ChainTransactionObserved per observed transaction. It runs after
// the WriteReport outcome is decided, regardless of whether the request succeeded, and never
// changes that outcome: failures are counted by the emitter and logged here. The request context
// may already be done when the request failed, so emission uses a detached context bounded by
// the emitter's own timeout.
func (e *EVM) emitTxEvents(ctx context.Context, lggr logger.SugaredLogger, md capabilities.RequestMetadata, observed []*txObservation) {
	if e.txEvents.Emitter == nil || len(observed) == 0 {
		return
	}
	if _, nop := e.txEvents.Emitter.(txevents.NopEmitter); nop {
		return
	}
	emitCtx := context.WithoutCancel(ctx)
	now := time.Now()
	for _, obs := range observed {
		if e.txEvents.TransmitterOnly && !obs.transmitter {
			continue
		}
		ev := buildTxEvent(obs, md, e.chainSelector, e.txEvents.CapabilityID, now)
		if err := e.txEvents.Emitter.Emit(emitCtx, ev); err != nil {
			if errors.Is(err, txevents.ErrEmitterUnavailable) {
				continue // logged once per process by the emitter
			}
			lggr.Errorw("Failed to emit ChainTransactionObserved", append(txevents.LogKVs(ev), "error", err.Error())...)
		}
	}
}
