package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

// defaultPageSize is the default page size for fetching transactions.
// This is chosen based on rpc response time benchmarks.
// And because the chances of an account submitting more than 10 transactions between rounds are very low.
const defaultPageSize = uint64(10)

// userTxData is a local struct matching the Go-default JSON output of
// aptos_api.UserTransaction (uppercase keys, numeric types). The SDK type has a
// custom UnmarshalJSON expecting Aptos REST-API format (lowercase keys,
// string-encoded numbers), which is incompatible with json.Marshal output.
type userTxData struct {
	Hash           string          `json:"Hash"`
	Success        bool            `json:"Success"`
	SequenceNumber uint64          `json:"SequenceNumber"`
	Timestamp      int64           `json:"Timestamp"` // micros since Unix epoch; int64 matches time.UnixMicro() and JSON numbers
	GasUsed        uint64          `json:"GasUsed"`
	GasUnitPrice   uint64          `json:"GasUnitPrice"`
	MaxGasAmount   uint64          `json:"MaxGasAmount"`
	VMStatus       string          `json:"VmStatus"`
	Payload        json.RawMessage `json:"Payload"`
}

type entryFunctionPayload struct {
	Inner struct {
		Function  string        `json:"Function"`
		Arguments []interface{} `json:"Arguments"`
	} `json:"Inner"`
}

// TxInfoRetriever finds matching forwarder report transactions on a transmitter account
// (success or failure) and returns tx hash plus gas / VM status for metering and replies.
type TxInfoRetriever struct {
	forwarderClient    CREForwarderClient
	lggr               logger.Logger
	transmissionID     TransmissionID
	entryFunctionName  string
	startingPointMicro int64
	report             *sdk.ReportResponse
	beholderProcessor  beholder.ProtoProcessor
	messageBuilder     *monitoring.MessageBuilder
	telemetryContext   monitoring.TelemetryContext
}

type TxInfoRetrieverOption func(*TxInfoRetriever)

func WithTxInfoRetrieverMonitoring(beholderProcessor beholder.ProtoProcessor, messageBuilder *monitoring.MessageBuilder, telemetryContext monitoring.TelemetryContext) TxInfoRetrieverOption {
	return func(thr *TxInfoRetriever) {
		thr.beholderProcessor = beholderProcessor
		thr.messageBuilder = messageBuilder
		thr.telemetryContext = telemetryContext
	}
}

type TxRetrievalResult string

const (
	TxRetrievalResultFound      TxRetrievalResult = "Found"
	TxRetrievalResultNotFound   TxRetrievalResult = "NotFound"
	TxRetrievalResultFetchError TxRetrievalResult = "FetchError"
)

type TxInfoLookupType string

const (
	LookupTypeSuccess TxInfoLookupType = "SuccessfulTransmission"
	LookupTypeFailed  TxInfoLookupType = "FailedTransmission"
)

type TxRetrievalPhase string

const (
	LastPagePoll   TxRetrievalPhase = "LastPagePoll"
	BackwardPoll   TxRetrievalPhase = "BackwardPoll"
	LatestPagePoll TxRetrievalPhase = "LatestPagePoll"
)

func NewTxInfoRetriever(forwarderClient CREForwarderClient, lggr logger.Logger, transmissionID TransmissionID, forwarderAddress string, requestStartTime time.Time, txSearchStartingBuffer time.Duration, report *sdk.ReportResponse, opts ...TxInfoRetrieverOption) TxInfoRetriever {
	retriever := TxInfoRetriever{
		forwarderClient:    forwarderClient,
		lggr:               logger.Named(lggr, "TxInfoRetriever"),
		transmissionID:     transmissionID,
		entryFunctionName:  fmt.Sprintf("%s::forwarder::report", forwarderAddress),
		startingPointMicro: requestStartTime.Add(-txSearchStartingBuffer).UnixMicro(),
		report:             report,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&retriever)
		}
	}
	lggr.Debugw("TxInfoRetriever created",
		"transmissionID", transmissionID.GetDebugID(),
		"entryFunctionName", retriever.entryFunctionName,
		"startingPointMicro", retriever.startingPointMicro,
	)
	return retriever
}

// TransmissionTxInfo is returned by GetSuccessfulTransmissionInfo and
// GetFailedTransmissionInfo. GasUsed and GasUnitPrice come from the matched
// UserTransaction and can be used to compute the fee in octas (GasUsed * GasUnitPrice).
type TransmissionTxInfo struct {
	TxHash         string
	GasUsed        uint64
	GasUnitPrice   uint64
	MaxGasAmount   uint64
	VMStatus       string
	BlockTimestamp uint64
}

// scanResult holds the output of scanTransactions: a matching tx hash (if found)
// and sequence/timestamp metadata from the scanned batch for pagination.
type scanResult struct {
	TransmissionTxInfo
	EarliestTsMicro int64
	MinSeqNum       uint64
	MaxSeqNum       uint64
}

// scanTransactions scans a batch of transactions for a matching forwarder::report call
// with the expected transmission ID in the raw_report argument.
// expectedSuccessValue filters by transaction success/failure status.
func (thr *TxInfoRetriever) scanTransactions(txns []*aptostypes.Transaction, expectedSuccessValue bool) scanResult {
	thr.lggr.Debugw("ScanTransactions called",
		"txCount", len(txns),
		"expectedSuccess", expectedSuccessValue,
	)
	var res scanResult
	metadataSet := false
	for _, tx := range txns {
		var userTx userTxData
		if unmarshalErr := json.Unmarshal(tx.Data, &userTx); unmarshalErr != nil {
			thr.lggr.Warnw("Failed to unmarshal user transaction, skipping", "err", unmarshalErr)
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
			continue
		}
		var payload entryFunctionPayload
		if unmarshalErr := json.Unmarshal(userTx.Payload, &payload); unmarshalErr != nil {
			thr.lggr.Debugw("ScanTransactions skipping tx - payload unmarshal failed",
				"txHash", userTx.Hash, "error", unmarshalErr,
			)
			continue
		}

		// match function name
		if payload.Inner.Function != thr.entryFunctionName {
			continue
		}

		// match report
		if thr.matchesTransmissionByReport(payload.Inner.Arguments) {
			thr.lggr.Debugw("Found matching transmission in scan",
				"txHash", userTx.Hash,
				"success", userTx.Success,
				"seqNum", userTx.SequenceNumber,
				"gasUsed", userTx.GasUsed,
				"gasUnitPrice", userTx.GasUnitPrice,
				"maxGasAmount", userTx.MaxGasAmount,
				"vmStatus", userTx.VMStatus,
				"blockTimestamp", userTx.Timestamp,
			)
			res.TransmissionTxInfo = TransmissionTxInfo{
				TxHash:       userTx.Hash,
				GasUsed:      userTx.GasUsed,
				GasUnitPrice: userTx.GasUnitPrice,
				MaxGasAmount: userTx.MaxGasAmount,
				VMStatus:     userTx.VMStatus,
			}
			if userTx.Timestamp < 0 {
				thr.lggr.Warnw("Invalid negative timestamp, skipping timestamp", "txHash", userTx.Hash, "timestamp", userTx.Timestamp)
			} else {
				res.BlockTimestamp = uint64(userTx.Timestamp)
			}
			return res
		}
	}

	// no match found
	thr.lggr.Debugw("ScanTransactions no match found",
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
func (thr *TxInfoRetriever) paginateBackwards(
	ctx context.Context,
	transmitter aptos_sdk.AccountAddress,
	scan txScanner,
	prevScanResult scanResult,
	pageSize uint64,
) (scanResult, error) {
	thr.lggr.Debugw("PaginateBackwards started",
		"transmitter", transmitter.String(),
		"earliestTxTimestamp", prevScanResult.EarliestTsMicro,
		"minSeqNum", prevScanResult.MinSeqNum,
		"startingPointMicro", thr.startingPointMicro,
		"pageSize", pageSize,
	)
	page := 0
	for prevScanResult.EarliestTsMicro > thr.startingPointMicro && prevScanResult.MinSeqNum > 0 {
		nextLimit := min(pageSize, prevScanResult.MinSeqNum)
		nextStart := prevScanResult.MinSeqNum - nextLimit
		thr.lggr.Debugw("Paginating backwards", "page", page, "nextStart", nextStart, "limit", nextLimit, "earliestTimestamp", prevScanResult.EarliestTsMicro, "startingPoint", thr.startingPointMicro)
		txns, err := capcommon.WithQuickRetry(ctx, thr.lggr, func(ctx context.Context) ([]*aptostypes.Transaction, error) {
			return thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, &nextStart, &nextLimit)
		})
		if err != nil {
			thr.lggr.Errorw("Pagination fetch failed", "page", page, "nextStart", nextStart, "error", err)
			return scanResult{}, fmt.Errorf("failed to get transmitter transactions during pagination (start=%d): %w", nextStart, err)
		}
		if len(txns) == 0 {
			thr.lggr.Debugw("Pagination got empty page, stopping", "page", page)
			break
		}

		// we want to scan here instead of keep fetching txns to meet the startingPointMicro
		// because we want to avoid fetching unnecessary txns and reduce i/o
		result := scan(txns)
		if result.TxHash != "" {
			thr.lggr.Debugw("Found match during pagination", "page", page, "txHash", result.TxHash)
			return result, nil
		}
		prevScanResult.EarliestTsMicro = result.EarliestTsMicro
		prevScanResult.MinSeqNum = result.MinSeqNum

		page++
		if nextStart == 0 {
			thr.lggr.Debugw("Reached sequence 0, stopping pagination")
			break
		}
	}
	thr.lggr.Debugw("PaginateBackwards completed with no match", "pages", page)
	return scanResult{}, nil
}

func (thr *TxInfoRetriever) emitTxInfoRetrievalPhase(ctx context.Context, lookupType TxInfoLookupType, phase TxRetrievalPhase, result TxRetrievalResult, phaseStart time.Time, txHash string, transmitter aptos_sdk.AccountAddress) {
	if thr.beholderProcessor == nil || thr.messageBuilder == nil {
		return
	}
	duration := time.Since(phaseStart)
	monitoring.EmitInitiated(ctx, thr.lggr, thr.beholderProcessor, thr.messageBuilder.BuildWriteReportTxInfoRetrievalPhase(
		thr.telemetryContext,
		string(phase),
		string(result),
		int64(math.Max(float64(duration.Milliseconds()), 0)),
		txHash, transmitter.String(), string(lookupType)))
}

// GetSuccessfulTransmissionInfo retrieves the tx hash of a successful report transmission
// by scanning the transmitter's account transactions.
//
// Three-phase approach:
//
//	Phase 1 (query latest): withQuickRetry fetch with nil limit so the RPC returns its
//	  default page; pageSize is derived from the response length. Empty results retried
//	  as likely RPC error.
//	Phase 2 (go back): paginate backwards through older transactions until our window
//	  covers startingPointMicro (requestArrivalTime - txSearchStartingBuffer), ensuring we haven't missed the tx.
//	Phase 3 (poll forward): history is covered; query forward from the max sequence number
//	  observed in Phase 1 (phase3Start = MaxSeqNum+1). Each poll advances the cursor so
//	  that new transactions submitted between phases cannot be missed even if the page
//	  would otherwise slide past them.
func (thr *TxInfoRetriever) GetSuccessfulTransmissionInfo(ctx context.Context, transmitter aptos_sdk.AccountAddress) (TransmissionTxInfo, error) {
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo called", "transmitter", transmitter.String())

	// Phase 1: fetch latest transactions with defaultPageSize to get the most recent page.
	phase1Start := time.Now()
	pageSize := defaultPageSize
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 1 - quick probe (pageSize)", "pageSize", pageSize)
	txns, err := capcommon.WithQuickRetry(ctx, thr.lggr, func(ctx context.Context) ([]*aptostypes.Transaction, error) {
		result, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, &pageSize)
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
		thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, LastPagePoll, TxRetrievalResultFetchError, phase1Start, "", transmitter)
		return TransmissionTxInfo{}, fmt.Errorf("failed to get transmitter transactions during phase 1: %w", err)
	}
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 1 fetched", "txCount", len(txns))
	phase1Result := thr.scanTransactions(txns, true)
	if phase1Result.TxHash != "" {
		thr.lggr.Debugw("GetSuccessfulTransmissionInfo found in phase 1", "txHash", phase1Result.TxHash)
		thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, LastPagePoll, TxRetrievalResultFound, phase1Start, phase1Result.TxHash, transmitter)
		return phase1Result.TransmissionTxInfo, nil
	}
	thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, LastPagePoll, TxRetrievalResultNotFound, phase1Start, "", transmitter)

	// Phase 2: paginate backwards until we cover the starting point
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 2 - paginate backwards",
		"earliestTxTimestamp", phase1Result.EarliestTsMicro, "startingPointMicro", thr.startingPointMicro,
		"firstSeqNum", phase1Result.MinSeqNum, "pageSize", pageSize)
	if phase1Result.EarliestTsMicro > thr.startingPointMicro {
		successScanner := func(txns []*aptostypes.Transaction) scanResult {
			return thr.scanTransactions(txns, true)
		}
		phase2Start := time.Now()
		if phase2Result, pgErr := thr.paginateBackwards(ctx, transmitter, successScanner, phase1Result, pageSize); pgErr != nil {
			thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, BackwardPoll, TxRetrievalResultFetchError, phase2Start, "", transmitter)
			thr.lggr.Warnw("GetSuccessfulTransmissionInfo phase 2 pagination failed, falling through to poll phase", "err", pgErr)
		} else if phase2Result.TxHash != "" {
			thr.lggr.Debugw("GetSuccessfulTransmissionInfo found in phase 2", "txHash", phase2Result.TxHash)
			thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, BackwardPoll, TxRetrievalResultFound, phase2Start, phase2Result.TxHash, transmitter)
			return phase2Result.TransmissionTxInfo, nil
		} else {
			thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, BackwardPoll, TxRetrievalResultNotFound, phase2Start, "", transmitter)
		}
	}

	// Phase 3: history covered, poll forward from the latest known sequence number with backoff until found or timeout.
	// This avoids the gap where new transactions between Phase 1 and Phase 3 could push
	// the target tx outside a fixed-size "latest" window.
	phase3Start := phase1Result.MaxSeqNum + 1
	thr.lggr.Debugw("GetSuccessfulTransmissionInfo phase 3 - poll forward", "phase3Start", phase3Start)
	phase3StartedAt := time.Now()
	phase3TerminalResult := TxRetrievalResultNotFound
	phase3Result, phase3Err := capcommon.WithPollingRetry(ctx, thr.lggr, func(ctx context.Context) (TransmissionTxInfo, error) {
		latestTxns, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, &phase3Start, nil)
		if fetchErr != nil {
			phase3TerminalResult = TxRetrievalResultFetchError
			return TransmissionTxInfo{}, fmt.Errorf("failed to get transmitter transactions during poll: %w", fetchErr)
		}
		if len(latestTxns) == 0 {
			phase3TerminalResult = TxRetrievalResultNotFound
			return TransmissionTxInfo{}, fmt.Errorf("no new transactions found for transmitter %s from seq %d", transmitter.String(), phase3Start)
		}
		result := thr.scanTransactions(latestTxns, true)
		if result.TxHash != "" {
			thr.lggr.Debugw("GetSuccessfulTransmissionInfo found in phase 3", "txHash", result.TxHash)
			return result.TransmissionTxInfo, nil
		}
		if result.MaxSeqNum >= phase3Start {
			phase3Start = result.MaxSeqNum + 1
		}
		phase3TerminalResult = TxRetrievalResultNotFound
		return TransmissionTxInfo{}, fmt.Errorf("matching transmission not found yet for %s", thr.transmissionID.GetDebugID())
	})
	if phase3Err != nil {
		thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, LatestPagePoll, phase3TerminalResult, phase3StartedAt, "", transmitter)
		return TransmissionTxInfo{}, phase3Err
	}
	thr.emitTxInfoRetrievalPhase(ctx, LookupTypeSuccess, LatestPagePoll, TxRetrievalResultFound, phase3StartedAt, phase3Result.TxHash, transmitter)
	return phase3Result, nil
}

// GetFailedTransmissionInfo searches a transmitter's transactions for a failed forwarder::report
// call matching this transmission ID. Two-phase approach (no polling phase), since the
// transmitting node may have crashed and we don't want to wait indefinitely.
//
//	Phase 1 (query latest): withQuickRetry fetch of the latest transactions,
//	  scan for a failed tx. Empty results retried as likely RPC error.
//	Phase 2 (go back): paginate backwards through older transactions until our window
//	  covers startingPointMicro (requestArrivalTime - txSearchStartingBuffer).
func (thr *TxInfoRetriever) GetFailedTransmissionInfo(ctx context.Context, transmitter aptos_sdk.AccountAddress) (TransmissionTxInfo, error) {
	thr.lggr.Debugw("GetFailedTransmissionInfo called", "transmitter", transmitter.String())

	// Phase 1: fetch latest transactions with no limit (nil) so the RPC returns its default page.
	// Derive pageSize from the response for phase 2.
	phase1Start := time.Now()
	pageSize := defaultPageSize
	thr.lggr.Debugw("GetFailedTransmissionInfo phase 1 - quick probe (pageSize)", "pageSize", pageSize)
	txns, err := capcommon.WithQuickRetry(ctx, thr.lggr, func(ctx context.Context) ([]*aptostypes.Transaction, error) {
		result, fetchErr := thr.forwarderClient.GetTransmitterTransactions(ctx, transmitter, nil, &pageSize)
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
		thr.emitTxInfoRetrievalPhase(ctx, LookupTypeFailed, LastPagePoll, TxRetrievalResultFetchError, phase1Start, "", transmitter)
		return TransmissionTxInfo{}, fmt.Errorf("failed to get transmitter transactions during phase 1: %w", err)
	}
	thr.lggr.Debugw("GetFailedTransmissionInfo phase 1 fetched", "txCount", len(txns))
	phase1Result := thr.scanTransactions(txns, false)
	if phase1Result.TxHash != "" {
		thr.lggr.Debugw("GetFailedTransmissionInfo found in phase 1", "txHash", phase1Result.TxHash)
		thr.emitTxInfoRetrievalPhase(ctx, LookupTypeFailed, LastPagePoll, TxRetrievalResultFound, phase1Start, phase1Result.TxHash, transmitter)
		return phase1Result.TransmissionTxInfo, nil
	}
	thr.emitTxInfoRetrievalPhase(ctx, LookupTypeFailed, LastPagePoll, TxRetrievalResultNotFound, phase1Start, "", transmitter)

	// Phase 2: paginate backwards only until we cover the starting point
	thr.lggr.Debugw("GetFailedTransmissionInfo phase 2 - paginate backwards",
		"earliestTxTimestamp", phase1Result.EarliestTsMicro, "startingPointMicro", thr.startingPointMicro,
		"firstSeqNum", phase1Result.MinSeqNum, "pageSize", pageSize)
	if phase1Result.EarliestTsMicro > thr.startingPointMicro {
		failureScanner := func(txns []*aptostypes.Transaction) scanResult {
			return thr.scanTransactions(txns, false)
		}
		phase2Start := time.Now()
		if phase2Result, pgErr := thr.paginateBackwards(ctx, transmitter, failureScanner, phase1Result, pageSize); pgErr != nil {
			thr.emitTxInfoRetrievalPhase(ctx, LookupTypeFailed, BackwardPoll, TxRetrievalResultFetchError, phase2Start, "", transmitter)
			thr.lggr.Warnw("GetFailedTransmissionInfo phase 2 pagination failed", "err", pgErr)
		} else if phase2Result.TxHash != "" {
			thr.lggr.Debugw("GetFailedTransmissionInfo found in phase 2", "txHash", phase2Result.TxHash)
			thr.emitTxInfoRetrievalPhase(ctx, LookupTypeFailed, BackwardPoll, TxRetrievalResultFound, phase2Start, phase2Result.TxHash, transmitter)
			return phase2Result.TransmissionTxInfo, nil
		} else {
			thr.emitTxInfoRetrievalPhase(ctx, LookupTypeFailed, BackwardPoll, TxRetrievalResultNotFound, phase2Start, "", transmitter)
		}
	}

	thr.lggr.Debugw("GetFailedTransmissionInfo no match found")
	return TransmissionTxInfo{}, fmt.Errorf("no matching failed transaction found for transmission %s", thr.transmissionID.GetDebugID())
}

// matchesTransmissionByReport checks if a transaction's receiver and raw_report
// match exactly what this node would submit.
func (thr *TxInfoRetriever) matchesTransmissionByReport(arguments []interface{}) bool {
	if len(arguments) < 3 {
		thr.lggr.Debugw("Payload mismatch: expected at least 3 arguments", "got", len(arguments))
		return false
	}

	receiverHex, _ := arguments[0].(string)
	expectedReceiverHex := hex.EncodeToString(thr.transmissionID.Receiver[:])
	if strings.TrimPrefix(receiverHex, "0x") != expectedReceiverHex {
		return false
	}

	reportHex, _ := arguments[1].(string)
	expectedReportHex := hex.EncodeToString(slices.Concat(thr.report.ReportContext, thr.report.RawReport))
	if strings.TrimPrefix(reportHex, "0x") != expectedReportHex {
		return false
	}

	return true
}
