package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
)

// TODO: Use config PLEX-2598
const (
	txSearchStartingBuffer = 1 * time.Minute
)

// userTxData is a local struct matching the Go-default JSON output of
// aptos_api.UserTransaction (uppercase keys, numeric types). The SDK type has a
// custom UnmarshalJSON expecting Aptos REST-API format (lowercase keys,
// string-encoded numbers), which is incompatible with json.Marshal output.
type userTxData struct {
	Hash           string          `json:"Hash"`
	Success        bool            `json:"Success"`
	SequenceNumber uint64          `json:"SequenceNumber"`
	Timestamp      uint64          `json:"Timestamp"`
	GasUsed        uint64          `json:"GasUsed"`
	GasUnitPrice   uint64          `json:"GasUnitPrice"`
	VMStatus       string          `json:"VMStatus"`
	Payload        json.RawMessage `json:"Payload"`
}

type entryFunctionPayload struct {
	Inner struct {
		Function  string        `json:"Function"`
		Arguments []interface{} `json:"Arguments"`
	} `json:"Inner"`
}

// TxHashRetriever scans a transmitter's account transactions to recover the
// canonical transaction for a report transmission, along with any fee-relevant
// data available on that transaction.
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
		lggr:               logger.Named(lggr, "TxHashRetriever"),
		transmissionID:     transmissionID,
		entryFunctionName:  fmt.Sprintf("%s::forwarder::report", forwarderAddress),
		startingPointMicro: requestStartTime.Add(-txSearchStartingBuffer).UnixMicro(),
	}
	lggr.Debugw("TxHashRetriever created",
		"transmissionID", transmissionID.GetDebugID(),
		"entryFunctionName", retriever.entryFunctionName,
		"startingPointMicro", retriever.startingPointMicro,
	)
	return retriever
}

// txInfo captures the matched transmission transaction and any fee-relevant data
// we can cheaply recover while scanning historical transactions.
type txInfo struct {
	TxHash       string
	GasUsed      uint64
	GasUnitPrice uint64
	VMStatus     string
}

// scanResult holds the output of scanTransactions: a matching tx (if found) and
// sequence/timestamp metadata from the scanned batch for pagination.
type scanResult struct {
	TxInfo          txInfo
	EarliestTsMicro uint64
	MinSeqNum       uint64
	MaxSeqNum       uint64
}

// scanTransactions scans a batch of transactions for a matching forwarder::report call
// with the expected transmission ID in the raw_report argument.
// expectedSuccessValue filters by transaction success/failure status.
func (thr *TxHashRetriever) scanTransactions(txns []*aptostypes.Transaction, expectedSuccessValue bool) scanResult {
	thr.lggr.Debugw("scanTransactions called",
		"txCount", len(txns),
		"expectedSuccess", expectedSuccessValue,
	)
	var res scanResult
	metadataSet := false
	for _, tx := range txns {
		var userTx userTxData
		if unmarshalErr := json.Unmarshal(tx.Data, &userTx); unmarshalErr != nil {
			thr.lggr.Warnw("failed to unmarshal user transaction, skipping", "err", unmarshalErr)
			continue
		}
		if !metadataSet {
			res.MinSeqNum = userTx.SequenceNumber
			res.MaxSeqNum = userTx.SequenceNumber
			res.EarliestTsMicro = userTx.Timestamp
			metadataSet = true
		}
		res.EarliestTsMicro = min(res.EarliestTsMicro, userTx.Timestamp)
		res.MinSeqNum = min(res.MinSeqNum, userTx.SequenceNumber)
		res.MaxSeqNum = max(res.MaxSeqNum, userTx.SequenceNumber)

		// match status
		if userTx.Success != expectedSuccessValue {
			thr.lggr.Debugw("scanTransactions skipping tx - success mismatch",
				"txHash", userTx.Hash, "txSuccess", userTx.Success, "expectedSuccess", expectedSuccessValue,
				"seqNum", userTx.SequenceNumber,
			)
			continue
		}
		var payload entryFunctionPayload
		if unmarshalErr := json.Unmarshal(userTx.Payload, &payload); unmarshalErr != nil {
			thr.lggr.Debugw("scanTransactions skipping tx - payload unmarshal failed",
				"txHash", userTx.Hash, "error", unmarshalErr,
			)
			continue
		}

		// match function name
		if payload.Inner.Function != thr.entryFunctionName {
			thr.lggr.Debugw("scanTransactions skipping tx - function mismatch",
				"txHash", userTx.Hash, "got", payload.Inner.Function, "want", thr.entryFunctionName,
			)
			continue
		}

		// match report
		if thr.matchesTransmissionByReport(payload.Inner.Arguments) {
			thr.lggr.Debugw("found matching transmission in scan",
				"txHash", userTx.Hash,
				"success", userTx.Success,
				"seqNum", userTx.SequenceNumber,
			)
			res.TxInfo = txInfo{
				TxHash:       userTx.Hash,
				GasUsed:      userTx.GasUsed,
				GasUnitPrice: userTx.GasUnitPrice,
				VMStatus:     userTx.VMStatus,
			}
			return res
		}
	}

	// no match found
	thr.lggr.Debugw("scanTransactions no match found",
		"earliestTsMicro", res.EarliestTsMicro,
		"minSeqNum", res.MinSeqNum,
		"maxSeqNum", res.MaxSeqNum,
	)
	return res
}

type txScanner func(txns []*aptostypes.Transaction) scanResult

// paginateBackwards fetches older transaction pages until the window covers startingPointMicro.
// Returns a scanResult with TxHash set if the scanner finds a match, or an empty TxHash if
// the window is covered with no match. The initial scanResult seeds the pagination cursor.
func (thr *TxHashRetriever) paginateBackwards(
	ctx context.Context,
	transmitter aptos_sdk.AccountAddress,
	scan txScanner,
	prevScanResult scanResult,
	pageSize uint64,
) (scanResult, error) {
	thr.lggr.Debugw("paginateBackwards started",
		"transmitter", transmitter.String(),
		"earliestTxTimestamp", prevScanResult.EarliestTsMicro,
		"minSeqNum", prevScanResult.MinSeqNum,
		"startingPointMicro", thr.startingPointMicro,
		"pageSize", pageSize,
	)
	page := 0
	startingPointMicro := uint64(0)
	if thr.startingPointMicro > 0 {
		startingPointMicro = uint64(thr.startingPointMicro) //nolint:gosec // guarded to positive int64 above
	}
	for prevScanResult.EarliestTsMicro > startingPointMicro && prevScanResult.MinSeqNum > 0 {
		var nextStart uint64
		if prevScanResult.MinSeqNum > pageSize {
			nextStart = prevScanResult.MinSeqNum - pageSize
		}
		thr.lggr.Debugw("paginating backwards", "page", page, "nextStart", nextStart, "earliestTimestamp", prevScanResult.EarliestTsMicro, "startingPoint", thr.startingPointMicro)

		txns, err := withQuickRetry(ctx, thr.lggr, func(ctx context.Context) ([]*aptostypes.Transaction, error) {
			return thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, &nextStart, &pageSize)
		})
		if err != nil {
			thr.lggr.Errorw("pagination fetch failed", "page", page, "nextStart", nextStart, "error", err)
			return scanResult{}, fmt.Errorf("failed to get transmitter transactions during pagination (start=%d): %w", nextStart, err)
		}
		if len(txns) == 0 {
			thr.lggr.Debugw("pagination got empty page, stopping", "page", page)
			break
		}

		// we want to scan here instead of keep fetching txns to meet the startingPointMicro
		// because we want to avoid fetching unnecessary txns and reduce i/o
		result := scan(txns)
		if result.TxInfo.TxHash != "" {
			thr.lggr.Debugw("found match during pagination", "page", page, "txHash", result.TxInfo.TxHash)
			return result, nil
		}
		prevScanResult.EarliestTsMicro = result.EarliestTsMicro
		prevScanResult.MinSeqNum = result.MinSeqNum

		page++
		if nextStart == 0 {
			thr.lggr.Debugw("reached sequence 0, stopping pagination")
			break
		}
	}
	thr.lggr.Debugw("paginateBackwards completed with no match", "pages", page)
	return scanResult{}, nil
}

// GetSuccessfulTransmissionInfo retrieves the canonical transaction info for a
// successful report transmission by scanning the transmitter's account transactions.
//
// Three-phase approach:
//
//	Phase 1 (query latest): withQuickRetry fetch with nil limit so the RPC returns its
//	  default page; pageSize is derived from the response length. Empty results retried
//	  as likely RPC error.
//	Phase 2 (go back): paginate backwards through older transactions until our window
//	  covers startingPointMicro (requestArrivalTime - 1 min), ensuring we haven't missed the tx.
//	Phase 3 (poll forward): history is covered; query forward from the max sequence number
//	  observed in Phase 1 (phase3Start = MaxSeqNum+1). Each poll advances the cursor so
//	  that new transactions submitted between phases cannot be missed even if the page
//	  would otherwise slide past them.
func (thr *TxHashRetriever) GetSuccessfulTransmissionInfo(ctx context.Context, transmitter aptos_sdk.AccountAddress) (txInfo, error) {
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo called", "transmitter", transmitter.String())

	// Phase 1: fetch latest transactions with no limit (nil) so the RPC returns its default page.
	// Derive pageSize from the response for subsequent phases.
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 1 - quick probe (nil limit)")
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
		thr.lggr.Warnw("GetSuccessfulTransmissionInfo phase 1 failed", "transmitter", transmitter.String(), "err", err)
		return txInfo{}, fmt.Errorf("failed to get transmitter transactions during phase 1: %w", err)
	}
	pageSize := uint64(len(txns))
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 1 fetched", "txCount", len(txns), "derivedPageSize", pageSize)
	phase1Result := thr.scanTransactions(txns, true)
	if phase1Result.TxInfo.TxHash != "" {
		thr.lggr.Debugw("GetSuccessfulTransmissionInfo found in phase 1", "txHash", phase1Result.TxInfo.TxHash)
		return phase1Result.TxInfo, nil
	}

	// Phase 2: paginate backwards until we cover the starting point
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 2 - paginate backwards",
		"earliestTxTimestamp", phase1Result.EarliestTsMicro, "startingPointMicro", thr.startingPointMicro,
		"firstSeqNum", phase1Result.MinSeqNum, "pageSize", pageSize)
	startingPointMicro := uint64(0)
	if thr.startingPointMicro > 0 {
		startingPointMicro = uint64(thr.startingPointMicro) //nolint:gosec // guarded to positive int64 above
	}
	if phase1Result.EarliestTsMicro > startingPointMicro {
		successScanner := func(txns []*aptostypes.Transaction) scanResult {
			return thr.scanTransactions(txns, true)
		}

		if phase2Result, pgErr := thr.paginateBackwards(ctx, transmitter, successScanner, phase1Result, pageSize); pgErr != nil {
			thr.lggr.Warnw("GetSuccessfulTransmissionInfo phase 2 pagination failed, falling through to poll phase", "err", pgErr)
		} else if phase2Result.TxInfo.TxHash != "" {
			thr.lggr.Debugw("GetSuccessfulTransmissionInfo found in phase 2", "txHash", phase2Result.TxInfo.TxHash)
			return phase2Result.TxInfo, nil
		}
	}

	// Phase 3: history covered, poll forward from the latest known sequence number with backoff until found or timeout.
	// This avoids the gap where new transactions between Phase 1 and Phase 3 could push
	// the target tx outside a fixed-size "latest" window.
	phase3Start := phase1Result.MaxSeqNum + 1
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 3 - poll forward", "phase3Start", phase3Start)
	return withPollingRetry(ctx, thr.lggr, func(ctx context.Context) (txInfo, error) {
		latestTxns, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, &phase3Start, nil)
		if fetchErr != nil {
			return txInfo{}, fmt.Errorf("failed to get transmitter transactions during poll: %w", fetchErr)
		}
		if len(latestTxns) == 0 {
			return txInfo{}, fmt.Errorf("no new transactions found for transmitter %s from seq %d", transmitter.String(), phase3Start)
		}
		result := thr.scanTransactions(latestTxns, true)
		if result.TxInfo.TxHash != "" {
			thr.lggr.Debugw("GetSuccessfulTransmissionInfo found in phase 3", "txHash", result.TxInfo.TxHash)
			return result.TxInfo, nil
		}
		if result.MaxSeqNum >= phase3Start {
			phase3Start = result.MaxSeqNum + 1
		}
		return txInfo{}, fmt.Errorf("matching transmission not found yet for %s", thr.transmissionID.GetDebugID())
	})
}

// GetFailedTransmissionInfo searches a transmitter's transactions for a failed
// forwarder::report call matching this transmission ID. Two-phase approach
// (no polling phase), since the transmitting node may have crashed and we don't
// want to wait indefinitely.
//
//	Phase 1 (query latest): withQuickRetry fetch of the latest transactions,
//	  scan for a failed tx. Empty results retried as likely RPC error.
//	Phase 2 (go back): paginate backwards through older transactions until our window
//	  covers startingPointMicro (requestArrivalTime - 1 min). The 1 min here will be passed through config PLEX-2598
func (thr *TxHashRetriever) GetFailedTransmissionInfo(ctx context.Context, transmitter aptos_sdk.AccountAddress) (txInfo, error) {
	thr.lggr.Debugw("GetFailedTransmissionInfo called", "transmitter", transmitter.String())

	// Phase 1: fetch latest transactions with no limit (nil) so the RPC returns its default page.
	// Derive pageSize from the response for phase 2.
	thr.lggr.Debugw("GetFailedTransmissionInfo phase 1 - quick probe (nil limit)")
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
		thr.lggr.Warnw("GetFailedTransmissionInfo phase 1 failed", "transmitter", transmitter.String(), "err", err)
		return txInfo{}, fmt.Errorf("failed to get transmitter transactions during phase 1: %w", err)
	}
	pageSize := uint64(len(txns))
	thr.lggr.Debugw("GetFailedTransmissionInfo phase 1 fetched", "txCount", len(txns), "derivedPageSize", pageSize)
	phase1Result := thr.scanTransactions(txns, false)
	if phase1Result.TxInfo.TxHash != "" {
		thr.lggr.Debugw("GetFailedTransmissionInfo found in phase 1", "txHash", phase1Result.TxInfo.TxHash)
		return phase1Result.TxInfo, nil
	}

	// Phase 2: paginate backwards only until we cover the starting point
	thr.lggr.Debugw("GetFailedTransmissionInfo phase 2 - paginate backwards",
		"earliestTxTimestamp", phase1Result.EarliestTsMicro, "startingPointMicro", thr.startingPointMicro,
		"firstSeqNum", phase1Result.MinSeqNum, "pageSize", pageSize)
	startingPointMicro := uint64(0)
	if thr.startingPointMicro > 0 {
		startingPointMicro = uint64(thr.startingPointMicro) //nolint:gosec // guarded to positive int64 above
	}
	if phase1Result.EarliestTsMicro > startingPointMicro {
		failureScanner := func(txns []*aptostypes.Transaction) scanResult {
			return thr.scanTransactions(txns, false)
		}
		if phase2Result, pgErr := thr.paginateBackwards(ctx, transmitter, failureScanner, phase1Result, pageSize); pgErr != nil {
			thr.lggr.Warnw("GetFailedTransmissionInfo phase 2 pagination failed", "err", pgErr)
		} else if phase2Result.TxInfo.TxHash != "" {
			thr.lggr.Debugw("GetFailedTransmissionInfo found in phase 2", "txHash", phase2Result.TxInfo.TxHash)
			return phase2Result.TxInfo, nil
		}
	}

	thr.lggr.Debugw("GetFailedTransmissionInfo no match found")
	return txInfo{}, fmt.Errorf("no matching failed transaction found for transmission %s", thr.transmissionID.GetDebugID())
}

// matchesTransmissionByReport checks if a transaction's raw_report argument
// contains the same transmission ID (workflow_execution_id + report_id) as ours.
//
// The entry function arguments for forwarder::report are: [receiver, raw_report, signatures].
// raw_report = report_context (96 bytes) || report, where report is decoded by ocrtypes.Decode.
func (thr *TxHashRetriever) matchesTransmissionByReport(arguments []interface{}) bool {
	if len(arguments) < 2 {
		thr.lggr.Debugw("matchesTransmissionByReport - not enough arguments", "argCount", len(arguments))
		return false
	}

	rawReportHex, ok := arguments[1].(string)
	if !ok {
		thr.lggr.Debugw("matchesTransmissionByReport - arg[1] not a string",
			"argType", fmt.Sprintf("%T", arguments[1]))
		return false
	}
	rawReport, err := hex.DecodeString(strings.TrimPrefix(rawReportHex, "0x"))
	if err != nil {
		thr.lggr.Debugw("matchesTransmissionByReport - hex decode failed", "error", err)
		return false
	}

	const reportContextLen = 96
	if len(rawReport) < reportContextLen {
		thr.lggr.Debugw("matchesTransmissionByReport - report too short", "len", len(rawReport))
		return false
	}
	report := rawReport[reportContextLen:]

	metadata, err := capcommon.DecodeReportMetadata(report)
	if err != nil {
		thr.lggr.Debugw("matchesTransmissionByReport - decodeReportMetadata failed", "error", err)
		return false
	}

	wantExecID := hex.EncodeToString(thr.transmissionID.WorkflowExecutionID[:])
	wantReportID := hex.EncodeToString(thr.transmissionID.ReportID[:])
	if metadata.ExecutionID != wantExecID {
		thr.lggr.Debugw("matchesTransmissionByReport - executionID mismatch",
			"got", metadata.ExecutionID, "want", wantExecID)
		return false
	}
	if metadata.ReportID != wantReportID {
		thr.lggr.Debugw("matchesTransmissionByReport - reportID mismatch",
			"gotReportID", metadata.ReportID, "wantReportID", wantReportID)
		return false
	}

	thr.lggr.Debugw("matchesTransmissionByReport - MATCH",
		"executionID", metadata.ExecutionID, "reportID", metadata.ReportID)
	return true
}
