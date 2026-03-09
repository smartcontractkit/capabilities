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
	retriever := TxHashRetriever{
		forwarderClient:    forwarderClient,
		lggr:               lggr,
		transmissionID:     transmissionID,
		entryFunctionName:  fmt.Sprintf("%s::forwarder::report", forwarderAddress),
		startingPointMicro: requestStartTime.Add(-txSearchStartingBuffer).UnixMicro(),
	}
	lggr.Infow("TestingAptosWriteCap: TxHashRetriever created",
		"transmissionID", transmissionID.GetDebugID(),
		"entryFunctionName", retriever.entryFunctionName,
		"startingPointMicro", retriever.startingPointMicro,
	)
	return retriever
}

// scanTransactions scans a batch of transactions for a matching forwarder::report call
// with the expected transmission ID in the raw_report argument.
// expectedSuccessValue filters by transaction success/failure status.
// Returns the matching tx hash if found, plus the earliest timestamp and first sequence number
// from the batch (used by the pagination/retry logic).
func (thr *TxHashRetriever) scanTransactions(txns []*aptostypes.Transaction, expectedSuccessValue bool) (txHash string, earliestTimestampMicro uint64, firstSequenceNumber uint64) {
	thr.lggr.Debugw("TestingAptosWriteCap: scanTransactions called",
		"txCount", len(txns),
		"expectedSuccess", expectedSuccessValue,
	)
	metadataSet := false
	for _, tx := range txns {
		userTx := aptos_api.UserTransaction{}
		if unmarshalErr := json.Unmarshal(tx.Data, &userTx); unmarshalErr != nil {
			thr.lggr.Warnw("TestingAptosWriteCap: failed to unmarshal user transaction, skipping", "err", unmarshalErr)
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
			thr.lggr.Infow("TestingAptosWriteCap: found matching transmission in scan",
				"txHash", string(userTx.Hash),
				"success", userTx.Success,
				"seqNum", userTx.SequenceNumber,
			)
			return string(userTx.Hash), earliestTimestampMicro, firstSequenceNumber
		}
	}
	thr.lggr.Debugw("TestingAptosWriteCap: scanTransactions no match found",
		"earliestTimestampMicro", earliestTimestampMicro,
		"firstSequenceNumber", firstSequenceNumber,
	)
	return "", earliestTimestampMicro, firstSequenceNumber
}

type txScanner func(txns []*aptostypes.Transaction) (txHash string, earliestTimestampMicro uint64, firstSequenceNumber uint64)

// paginateBackwards fetches older transaction pages until the window covers startingPointMicro.
// Returns the matching tx hash if the scanner finds one, or ("", nil) if the window is covered
// with no match. earliestTs/firstSeqNum are from the initial scan of the latest page.
func (thr *TxHashRetriever) paginateBackwards(
	ctx context.Context,
	transmitter aptos_sdk.AccountAddress,
	scan txScanner,
	earliestTs uint64,
	firstSeqNum uint64,
	pageSize uint64,
) (string, error) {
	thr.lggr.Infow("TestingAptosWriteCap: paginateBackwards started",
		"transmitter", transmitter.String(),
		"earliestTs", earliestTs,
		"firstSeqNum", firstSeqNum,
		"startingPointMicro", thr.startingPointMicro,
		"pageSize", pageSize,
	)
	page := 0
	for earliestTs > uint64(thr.startingPointMicro) && firstSeqNum > 0 {
		var nextStart uint64
		if firstSeqNum > pageSize {
			nextStart = firstSeqNum - pageSize
		}
		thr.lggr.Debugw("TestingAptosWriteCap: paginating backwards", "page", page, "nextStart", nextStart, "earliestTimestamp", earliestTs, "startingPoint", thr.startingPointMicro)

		txns, err := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, &nextStart, &pageSize)
		if err != nil {
			thr.lggr.Errorw("TestingAptosWriteCap: pagination fetch failed", "page", page, "nextStart", nextStart, "error", err)
			return "", fmt.Errorf("failed to get transmitter transactions during pagination (start=%d): %w", nextStart, err)
		}
		if len(txns) == 0 {
			thr.lggr.Debugw("TestingAptosWriteCap: pagination got empty page, stopping", "page", page)
			break
		}

		// we want to scan here instead of keep fetching txns to meet the startingPointMicro
		// because we want to avoid fetching unnecessary txns and reduce i/o
		var txHash string
		txHash, earliestTs, firstSeqNum = scan(txns)
		if txHash != "" {
			thr.lggr.Infow("TestingAptosWriteCap: found match during pagination", "page", page, "txHash", txHash)
			return txHash, nil
		}

		page++
		if nextStart == 0 {
			thr.lggr.Debugw("TestingAptosWriteCap: reached sequence 0, stopping pagination")
			break
		}
	}
	thr.lggr.Debugw("TestingAptosWriteCap: paginateBackwards completed with no match", "pages", page)
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
	thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash called", "transmitter", transmitter.String())

	// Phase 1: fetch latest transactions with no limit (nil) so the RPC returns its default page.
	// Derive pageSize from the response for subsequent phases.
	thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash phase 1 - quick probe (nil limit)")
	txns, err := withQuickRetry(ctx, thr.lggr, func(ctx context.Context) ([]*aptostypes.Transaction, error) {
		result, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, nil)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("no transactions found for transmitter %s, possible RPC issue", transmitter.String())
		}
		return result, nil
	})
	if err != nil {
		thr.lggr.Warnw("TestingAptosWriteCap: GetSuccessfulTransmissionHash phase 1 failed", "transmitter", transmitter.String(), "err", err)
		return "", fmt.Errorf("failed to get transmitter transactions during phase 1: %w", err)
	}
	pageSize := uint64(len(txns))
	thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash phase 1 fetched", "txCount", len(txns), "derivedPageSize", pageSize)
	txHash, earliestTxTimestamp, firstSeqNum := thr.scanTransactions(txns, true)
	if txHash != "" {
		thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash found in phase 1", "txHash", txHash)
		return txHash, nil
	}

	// Phase 2: paginate backwards until we cover the starting point
	thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash phase 2 - paginate backwards",
		"earliestTxTimestamp", earliestTxTimestamp, "startingPointMicro", thr.startingPointMicro, "firstSeqNum", firstSeqNum, "pageSize", pageSize)
	if earliestTxTimestamp > uint64(thr.startingPointMicro) {
		successScanner := func(txns []*aptostypes.Transaction) (string, uint64, uint64) {
			return thr.scanTransactions(txns, true)
		}
		// TODO: emit metrics here to see if we need to adjust initial batch size
		if hash, pgErr := thr.paginateBackwards(ctx, transmitter, successScanner, earliestTxTimestamp, firstSeqNum, pageSize); pgErr != nil {
			thr.lggr.Warnw("TestingAptosWriteCap: GetSuccessfulTransmissionHash phase 2 pagination failed, falling through to poll phase", "err", pgErr)
		} else if hash != "" {
			thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash found in phase 2", "txHash", hash)
			return hash, nil
		}
	}

	// Phase 3: history covered, poll latest with backoff until found or timeout.
	// Uses withPollingRetry for structured retry logging, MaxRetries cap, and 60s timeout.
	thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash phase 3 - poll latest")
	return withPollingRetry(ctx, thr.lggr, func(ctx context.Context) (string, error) {
		latestTxns, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, &pageSize)
		if fetchErr != nil {
			return "", fmt.Errorf("failed to get transmitter transactions during poll: %w", fetchErr)
		}
		if len(latestTxns) == 0 {
			return "", fmt.Errorf("no transactions found for transmitter %s", transmitter.String())
		}
		hash, _, _ := thr.scanTransactions(latestTxns, true)
		if hash == "" {
			return "", fmt.Errorf("matching transmission not found yet for %s", thr.transmissionID.GetDebugID())
		}
		thr.lggr.Infow("TestingAptosWriteCap: GetSuccessfulTransmissionHash found in phase 3", "txHash", hash)
		return hash, nil
	})
}

// GetFailedTransmissionHash searches a transmitter's transactions for a failed forwarder::report
// call matching this transmission ID. Two-phase approach (no polling phase), since the
// transmitting node may have crashed and we don't want to wait indefinitely.
//
//	Phase 1 (query latest): withQuickRetry fetch of the latest pageSize/2 transactions,
//	  scan for a failed tx. Empty results retried as likely RPC error.
//	Phase 2 (go back): paginate backwards through older transactions until our window
//	  covers startingPointMicro (requestArrivalTime - 1 min).
func (thr *TxHashRetriever) GetFailedTransmissionHash(ctx context.Context, transmitter aptos_sdk.AccountAddress) (string, error) {
	thr.lggr.Infow("TestingAptosWriteCap: GetFailedTransmissionHash called", "transmitter", transmitter.String())

	// Phase 1: fetch latest transactions with no limit (nil) so the RPC returns its default page.
	// Derive pageSize from the response for phase 2.
	thr.lggr.Infow("TestingAptosWriteCap: GetFailedTransmissionHash phase 1 - quick probe (nil limit)")
	txns, err := withQuickRetry(ctx, thr.lggr, func(ctx context.Context) ([]*aptostypes.Transaction, error) {
		result, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, nil)
		if fetchErr != nil {
			return nil, fetchErr
		}
		if len(result) == 0 {
			return nil, fmt.Errorf("no transactions found for transmitter %s, possible RPC issue", transmitter.String())
		}
		return result, nil
	})
	if err != nil {
		thr.lggr.Warnw("TestingAptosWriteCap: GetFailedTransmissionHash phase 1 failed", "transmitter", transmitter.String(), "err", err)
		return "", fmt.Errorf("failed to get transmitter transactions during phase 1: %w", err)
	}
	pageSize := uint64(len(txns))
	thr.lggr.Infow("TestingAptosWriteCap: GetFailedTransmissionHash phase 1 fetched", "txCount", len(txns), "derivedPageSize", pageSize)
	txHash, earliestTxTimestamp, firstSeqNum := thr.scanTransactions(txns, false)
	if txHash != "" {
		thr.lggr.Infow("TestingAptosWriteCap: GetFailedTransmissionHash found in phase 1", "txHash", txHash)
		return txHash, nil
	}

	// Phase 2: paginate backwards only until we cover the starting point
	thr.lggr.Infow("TestingAptosWriteCap: GetFailedTransmissionHash phase 2 - paginate backwards",
		"earliestTxTimestamp", earliestTxTimestamp, "startingPointMicro", thr.startingPointMicro, "firstSeqNum", firstSeqNum, "pageSize", pageSize)
	if earliestTxTimestamp > uint64(thr.startingPointMicro) {
		failureScanner := func(txns []*aptostypes.Transaction) (string, uint64, uint64) {
			return thr.scanTransactions(txns, false)
		}
		if hash, pgErr := thr.paginateBackwards(ctx, transmitter, failureScanner, earliestTxTimestamp, firstSeqNum, pageSize); pgErr != nil {
			thr.lggr.Warnw("TestingAptosWriteCap: GetFailedTransmissionHash phase 2 pagination failed", "err", pgErr)
		} else if hash != "" {
			thr.lggr.Infow("TestingAptosWriteCap: GetFailedTransmissionHash found in phase 2", "txHash", hash)
			return hash, nil
		}
	}

	thr.lggr.Infow("TestingAptosWriteCap: GetFailedTransmissionHash no match found")
	return "", fmt.Errorf("no matching failed transaction found for transmission %s", thr.transmissionID.GetDebugID())
}

// matchesTransmissionByReport checks if a transaction's raw_report argument
// contains the same transmission ID (workflow_execution_id + report_id) as ours.
//
// The entry function arguments for forwarder::report are: [receiver, raw_report, signatures].
// raw_report = report_context (96 bytes) || report, where report is decoded by ocrtypes.Decode.
func (thr *TxHashRetriever) matchesTransmissionByReport(entryFunction *aptos_api.TransactionPayloadEntryFunction) bool {
	if len(entryFunction.Arguments) < 2 {
		thr.lggr.Debugw("TestingAptosWriteCap: matchesTransmissionByReport - not enough arguments", "argCount", len(entryFunction.Arguments))
		return false
	}

	rawReportHex, ok := entryFunction.Arguments[1].(string)
	if !ok {
		return false
	}
	rawReport, err := hex.DecodeString(strings.TrimPrefix(rawReportHex, "0x"))
	if err != nil {
		thr.lggr.Debugw("TestingAptosWriteCap: matchesTransmissionByReport - hex decode failed", "error", err)
		return false
	}

	const reportContextLen = 96
	if len(rawReport) < reportContextLen {
		thr.lggr.Debugw("TestingAptosWriteCap: matchesTransmissionByReport - report too short", "len", len(rawReport))
		return false
	}
	report := rawReport[reportContextLen:]

	metadata, err := decodeReportMetadata(report)
	if err != nil {
		thr.lggr.Debugw("TestingAptosWriteCap: matchesTransmissionByReport - decodeReportMetadata failed", "error", err)
		return false
	}

	if metadata.ExecutionID != hex.EncodeToString(thr.transmissionID.WorkflowExecutionID[:]) {
		return false
	}
	if metadata.ReportID != hex.EncodeToString(thr.transmissionID.ReportID[:]) {
		return false
	}

	thr.lggr.Debugw("TestingAptosWriteCap: matchesTransmissionByReport - MATCH",
		"executionID", metadata.ExecutionID, "reportID", metadata.ReportID)
	return true
}
