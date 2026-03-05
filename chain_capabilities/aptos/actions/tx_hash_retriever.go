package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	aptos_api "github.com/aptos-labs/aptos-go-sdk/api"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
)

// TODO: where does all this config get passed in from ?
const (
	txSearchPageSize       = uint64(100)
	txSearchStartingBuffer = 1 * time.Minute
)

// TxHashRetriever retrieves the transaction hash for a report transmission
// by scanning the transmitter's account transactions.
type TxHashRetriever struct {
	forwarderClient    CREForwarderClient
	lggr               logger.Logger
	transmissionID     TransmissionID
	entryFunctionName  string
	startingPointMicro int64
}

func NewTxHashRetriever(forwarderClient CREForwarderClient, lggr logger.Logger, transmissionID TransmissionID, forwarderAddress string, requestStartTime time.Time) TxHashRetriever {
	return TxHashRetriever{
		forwarderClient:    forwarderClient,
		lggr:               lggr,
		transmissionID:     transmissionID,
		entryFunctionName:  fmt.Sprintf("%s::forwarder::report", forwarderAddress),
		startingPointMicro: requestStartTime.Add(-txSearchStartingBuffer).UnixMicro(),
	}
}

// scanTransactions scans a batch of transactions for a matching forwarder::report call
// with the expected transmission ID in the raw_report argument.
// expectedSuccessValue filters by transaction success/failure status.
// Returns the matching tx hash if found, plus the earliest timestamp and first sequence number
// from the batch (used by the pagination/retry logic).
func (thr *TxHashRetriever) scanTransactions(txns []*aptostypes.Transaction, expectedSuccessValue bool) (txHash string, earliestTimestampMicro uint64, firstSequenceNumber uint64, err error) {
	metadataSet := false
	for _, tx := range txns {
		userTx := aptos_api.UserTransaction{}
		if unmarshalErr := json.Unmarshal(tx.Data, &userTx); unmarshalErr != nil {
			thr.lggr.Warnw("Failed to unmarshal user transaction, skipping", "err", unmarshalErr)
			continue
		}
		// Set pagination metadata from the first successfully unmarshalled tx
		if !metadataSet {
			firstSequenceNumber = userTx.SequenceNumber
			earliestTimestampMicro = userTx.Timestamp
			metadataSet = true
		}
		if userTx.Timestamp < earliestTimestampMicro {
			earliestTimestampMicro = userTx.Timestamp
		}
		if userTx.SequenceNumber < firstSequenceNumber {
			firstSequenceNumber = userTx.SequenceNumber
		}
		if userTx.Success != expectedSuccessValue {
			continue
		}
		entryFunction, ok := userTx.Payload.Inner.(*aptos_api.TransactionPayloadEntryFunction)
		if !ok {
			continue
		} else if entryFunction.Function != thr.entryFunctionName {
			continue
		} else if thr.matchesTransmissionByReport(entryFunction) {
			return string(userTx.Hash), earliestTimestampMicro, firstSequenceNumber, nil
		}
	}
	return "", earliestTimestampMicro, firstSequenceNumber, nil
}

type txScanner func(txns []*aptostypes.Transaction) (txHash string, earliestTimestampMicro uint64, firstSequenceNumber uint64, err error)

// paginateBackwards fetches older transaction pages until the window covers startingPointMicro.
// Returns the matching tx hash if the scanner finds one, or ("", nil) if the window is covered
// with no match. earliestTs/firstSeqNum are from the initial scan of the latest page.
func (thr *TxHashRetriever) paginateBackwards(
	ctx context.Context,
	transmitter aptos_sdk.AccountAddress,
	scan txScanner,
	earliestTs uint64,
	firstSeqNum uint64,
) (string, error) {
	pageSize := txSearchPageSize
	for earliestTs > uint64(thr.startingPointMicro) && firstSeqNum > 0 {
		var nextStart uint64
		if firstSeqNum > pageSize {
			nextStart = firstSeqNum - pageSize
		}
		thr.lggr.Debugw("Paginating backwards", "nextStart", nextStart, "earliestTimestamp", earliestTs, "startingPoint", thr.startingPointMicro)

		txns, err := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, &nextStart, &pageSize)
		if err != nil {
			return "", fmt.Errorf("failed to get transmitter transactions during pagination (start=%d): %w", nextStart, err)
		}
		if len(txns) == 0 {
			break
		}

		var txHash string
		var scanErr error
		// we want to scan here instead of keep fetching txns to meet the startingPointMicro
		// because we want to avoid fetching unnecessary txns and reduce i/o
		txHash, earliestTs, firstSeqNum, scanErr = scan(txns)
		if scanErr != nil {
			return "", scanErr
		}
		if txHash != "" {
			return txHash, nil
		}

		if nextStart == 0 {
			break
		}
	}
	return "", nil
}

// GetSuccessfulTransmissionHash retrieves the tx hash of a successful report transmission
// by scanning the transmitter's account transactions.
//
// Three-phase approach:
//
//	Phase 1 (query latest): withQuickRetry fetch of the latest pageSize/2 transactions,
//	  scan for the event. Empty results retried as likely RPC error.
//	Phase 2 (go back): paginate backwards through older transactions until our window
//	  covers startingPointMicro (requestArrivalTime - 1 min), ensuring we haven't missed the tx.
//	Phase 3 (poll latest): history is covered, keep re-querying latest transactions
//	  with withPollingRetry until the tx appears or timeout.
func (thr *TxHashRetriever) GetSuccessfulTransmissionHash(ctx context.Context, transmitter aptos_sdk.AccountAddress) (string, error) {
	pageSize := txSearchPageSize
	halfPage := pageSize / 2

	// Phase 1: quick probe of the latest transactions (half page, with retry).
	// Empty results are treated as a retryable error -- a transmitter account
	// should always have transactions; an empty response likely indicates an RPC issue.
	txns, err := withQuickRetry(ctx, thr.lggr, func(ctx context.Context) ([]*aptostypes.Transaction, error) {
		result, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, &halfPage)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("no transactions found for transmitter %s, possible RPC issue", transmitter.String())
		}
		return result, nil
	})
	if err != nil {
		thr.lggr.Warnw("Phase 1 failed after retries, skipping to poll phase", "transmitter", transmitter.String(), "err", err)
		return "", fmt.Errorf("failed to get transmitter transactions during phase 1: %w", err)
	}
	txHash, earliestTxTimestamp, firstSeqNum, scanErr := thr.scanTransactions(txns, true)
	if scanErr != nil {
		return "", scanErr
	}
	if txHash != "" {
		return txHash, nil
	}

	// Phase 2: paginate backwards until we cover the starting point
	if earliestTxTimestamp > uint64(thr.startingPointMicro) {
		successScanner := func(txns []*aptostypes.Transaction) (string, uint64, uint64, error) {
			return thr.scanTransactions(txns, true)
		}
		// TODO: emit metrics here to see if we need to adjust initial batch size
		if hash, pgErr := thr.paginateBackwards(ctx, transmitter, successScanner, earliestTxTimestamp, firstSeqNum); pgErr != nil {
			thr.lggr.Warnw("Phase 2 pagination failed, falling through to poll phase", "err", pgErr)
		} else if hash != "" {
			return hash, nil
		}
	}

	// Phase 3: history covered, poll latest with backoff until found or timeout.
	// Uses withPollingRetry for structured retry logging, MaxRetries cap, and 60s timeout.
	return withPollingRetry(ctx, thr.lggr, func(ctx context.Context) (string, error) {
		latestTxns, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, &pageSize)
		if fetchErr != nil {
			return "", fmt.Errorf("failed to get transmitter transactions during poll: %w", fetchErr)
		}
		if len(latestTxns) == 0 {
			return "", fmt.Errorf("no transactions found for transmitter %s", transmitter.String())
		}
		hash, _, _, scanErr := thr.scanTransactions(latestTxns, true)
		if scanErr != nil {
			return "", scanErr
		}
		if hash == "" {
			return "", fmt.Errorf("matching transmission not found yet for %s", thr.transmissionID.GetDebugID())
		}
		return hash, nil
	})
}

// GetFailedTransmissionHash searches a transmitter's transactions for a failed forwarder::report
// call matching this transmission ID. Only paginates backwards (no polling phase), since the
// transmitting node may have crashed and we don't want to wait indefinitely.
func (thr *TxHashRetriever) GetFailedTransmissionHash(ctx context.Context, transmitter aptos_sdk.AccountAddress) (string, error) {
	pageSize := txSearchPageSize

	txns, err := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, &pageSize)
	if err != nil {
		return "", fmt.Errorf("failed to get transmitter transactions: %w", err)
	}
	if len(txns) == 0 {
		return "", fmt.Errorf("no transactions found for transmitter %s", transmitter.String())
	}

	txHash, earliestTxTimestamp, firstSeqNum, scanErr := thr.scanTransactions(txns, false)
	if scanErr != nil {
		return "", scanErr
	}
	if txHash != "" {
		return txHash, nil
	}

	// Paginate backwards only until we cover the starting point
	if earliestTxTimestamp > uint64(thr.startingPointMicro) {
		failureScanner := func(txns []*aptostypes.Transaction) (string, uint64, uint64, error) {
			return thr.scanTransactions(txns, false)
		}
		if hash, pgErr := thr.paginateBackwards(ctx, transmitter, failureScanner, earliestTxTimestamp, firstSeqNum); pgErr != nil {
			thr.lggr.Warnw("Failed to paginate backwards for failed transmission search", "err", pgErr)
		} else if hash != "" {
			return hash, nil
		}
	}

	return "", fmt.Errorf("no matching failed transaction found for transmission %s", thr.transmissionID.GetDebugID())
}

// matchesTransmissionByReport checks if a transaction's raw_report argument
// contains the same transmission ID (workflow_execution_id + report_id) as ours.
//
// The entry function arguments for forwarder::report are: [receiver, raw_report, signatures].
// raw_report = report_context (96 bytes) || report, where report is decoded by ocrtypes.Decode.
func (thr *TxHashRetriever) matchesTransmissionByReport(entryFunction *aptos_api.TransactionPayloadEntryFunction) bool {
	if len(entryFunction.Arguments) < 2 {
		return false
	}

	rawReportHex, ok := entryFunction.Arguments[1].(string)
	if !ok {
		return false
	}
	rawReport, err := hex.DecodeString(strings.TrimPrefix(rawReportHex, "0x"))
	if err != nil {
		return false
	}

	const reportContextLen = 96
	if len(rawReport) < reportContextLen {
		return false
	}
	report := rawReport[reportContextLen:]

	metadata, err := decodeReportMetadata(report)
	if err != nil {
		return false
	}

	if metadata.ExecutionID != hex.EncodeToString(thr.transmissionID.WorkflowExecutionID[:]) {
		return false
	}
	if metadata.ReportID != hex.EncodeToString(thr.transmissionID.ReportID[:]) {
		return false
	}

	return true
}
