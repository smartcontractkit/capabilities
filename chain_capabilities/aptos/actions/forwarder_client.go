package actions

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/aptos-labs/aptos-go-sdk/bcs"
	aptos_forwarder "github.com/smartcontractkit/chainlink-aptos/bindings/platform/forwarder"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
)

// CREForwarderClient abstracts the interaction with the Aptos CRE forwarder contract.
type CREForwarderClient interface {
	// InvokeOnReport builds and submits a forwarder report transaction to the Aptos chain.
	InvokeOnReport(ctx context.Context, receiver []byte, report *sdk.ReportResponse, gasConfig *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error)
	// GetTransmissionInfo queries the forwarder contract for the transmission state of a given transmission ID.
	GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error)
	// GetTransmissionTxHash resolves the canonical tx hash for a successful transmission.
	GetTransmissionTxHash(ctx context.Context, transmissionID TransmissionID, transmitter string, expectedForwarderRawReport []byte) (string, error)
	// ValidateFailedTxHash validates a candidate failed tx hash onchain and ensures it matches this transmission.
	ValidateFailedTxHash(ctx context.Context, transmissionID TransmissionID, txHash string, expectedForwarderRawReport []byte) (string, error)
	// GetTransmissionFailedTxHash resolves a deterministic failed tx hash for an invalid transmission attempt.
	GetTransmissionFailedTxHash(ctx context.Context, transmissionID TransmissionID, transmitters []string, maxLedgerVersion *uint64) (string, error)
}

type forwarderClient struct {
	types.AptosService
	lggr             logger.Logger
	forwarderAddress [32]byte
	forwarderEncoder aptos_forwarder.ForwarderEncoder
}

const (
	maxAccountTransactionsPageSize uint64 = 45
	// TODO: make canonical tx-hash lookback configurable once CRE config
	// plumbing is finalized and stable across local/remote test runs.
	defaultTxHashLookback uint64 = 225
)

type successfulTxScanStats struct {
	pagesScanned              uint64
	transactionsScanned       uint64
	nilTransactions           uint64
	nonSuccessfulTransactions uint64
	decodeFailures            uint64
	nonForwarderReportCalls   uint64
	missingReportProcessed    uint64
	emptyHashCandidates       uint64
	decodeFailureSamples      []string
}

func (s *successfulTxScanStats) recordDecodeFailure(hash string, err error) {
	s.decodeFailures++
	if len(s.decodeFailureSamples) >= 3 {
		return
	}
	h := strings.TrimSpace(hash)
	if h == "" {
		h = "<empty-hash>"
	}
	s.decodeFailureSamples = append(s.decodeFailureSamples, fmt.Sprintf("%s: %v", h, err))
}

func (s successfulTxScanStats) summary() string {
	summary := fmt.Sprintf(
		"pages=%d txs=%d nil_txs=%d non_success=%d decode_failures=%d non_forwarder=%d missing_report_processed=%d empty_hash_candidates=%d",
		s.pagesScanned,
		s.transactionsScanned,
		s.nilTransactions,
		s.nonSuccessfulTransactions,
		s.decodeFailures,
		s.nonForwarderReportCalls,
		s.missingReportProcessed,
		s.emptyHashCandidates,
	)
	if len(s.decodeFailureSamples) == 0 {
		return summary
	}
	return fmt.Sprintf("%s decode_samples=[%s]", summary, strings.Join(s.decodeFailureSamples, "; "))
}

func newForwarderClient(aptosService types.AptosService, lggr logger.Logger, forwarderAddress [32]byte) CREForwarderClient {
	emptyClient := aptos_sdk.Client{}
	forwarder := aptos_forwarder.NewForwarder(forwarderAddress, &emptyClient)
	forwarderEncoder := forwarder.Encoder()
	return &forwarderClient{
		AptosService:     aptosService,
		lggr:             lggr,
		forwarderAddress: forwarderAddress,
		forwarderEncoder: forwarderEncoder,
	}
}

func (fc *forwarderClient) InvokeOnReport(ctx context.Context, receiver []byte, report *sdk.ReportResponse, gasConfig *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error) {
	// use receiver address
	// use report.RawReport
	// the report.RawReport is what we came to consensus on
	// the report.RawReport has the client payload wrapped inside it and a bunch of other stuff
	// the forwarder contract is responsible for unwrapping the client payload and forwarding it to the receiver
	// use report.sigs
	// encode that as a report call on the forwarder contract

	var signatures [][]byte
	for _, sig := range report.Sigs {
		signatures = append(signatures, sig.Signature)
	}

	rawReport := report.RawReport
	if len(report.ReportContext) > 0 {
		if len(report.ReportContext) != 96 {
			return nil, fmt.Errorf("invalid report context length: got %d want 96", len(report.ReportContext))
		}
		// Aptos forwarder validates signatures over blake2b(report_context || report)
		// and parses report bytes starting at offset 96.
		rawReport = make([]byte, 0, len(report.ReportContext)+len(report.RawReport))
		rawReport = append(rawReport, report.ReportContext...)
		rawReport = append(rawReport, report.RawReport...)
	}

	receiverAddress := aptos_sdk.AccountAddress(receiver)
	moduleInformation, _, argTypes, args, err := fc.forwarderEncoder.Report(receiverAddress, rawReport, signatures)
	if err != nil {
		return nil, fmt.Errorf("failed to encode forwarder report: %w", err)
	}

	payload := aptos_sdk.TransactionPayload{
		Payload: &aptos_sdk.EntryFunction{
			Module: aptos_sdk.ModuleId{
				Address: moduleInformation.Address,
				Name:    moduleInformation.ModuleName,
			},
			Function: "report",
			ArgTypes: argTypes,
			Args:     args,
		},
	}
	encodedPayload, err := bcs.Serialize(&payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal forwarder report payload: %w", err)
	}

	var resolvedGasConfig *aptostypes.GasConfig
	if gasConfig != nil {
		resolvedGasConfig = &aptostypes.GasConfig{
			MaxGasAmount: gasConfig.MaxGasAmount,
			GasUnitPrice: gasConfig.GasUnitPrice,
		}
	}

	reply, err := fc.AptosService.SubmitTransaction(ctx, aptostypes.SubmitTransactionRequest{
		// TODO: do i really need ReceiverModuleID if my EncodedPayload is of type EntryFunction which has all the details ?
		ReceiverModuleID: aptostypes.ModuleID{
			Address: aptostypes.AccountAddress(fc.forwarderAddress),
			Name:    moduleInformation.ModuleName,
		},
		EncodedPayload: encodedPayload,
		GasConfig:      resolvedGasConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to submit forwarder report transaction: %w", err)
	}

	return reply, nil
}

type TransmissionID struct {
	Receiver            aptos_sdk.AccountAddress
	ReportID            [2]byte
	WorkflowExecutionID [32]byte
}

type TransmissionInfo struct {
	Success     bool
	Transmitter string
}

// accountTransactionsReader is an optional extension implemented by some Aptos clients.
// It lets us find canonical tx hash from the winning transmitter account history.
type accountTransactionsReader interface {
	AccountTransactions(ctx context.Context, req aptostypes.AccountTransactionsRequest) (*aptostypes.AccountTransactionsReply, error)
}

func (fc *forwarderClient) GetTransmissionInfo(ctx context.Context, transmissionID TransmissionID) (TransmissionInfo, error) {
	// Convert [2]byte report ID to uint16 (big-endian, as stored in report metadata)
	reportID := binary.BigEndian.Uint16(transmissionID.ReportID[:])

	// Use the encoder to get the BCS-encoded view call arguments
	moduleInfo, functionName, _, args, err := fc.forwarderEncoder.GetTransmissionState(
		transmissionID.Receiver,
		transmissionID.WorkflowExecutionID[:],
		reportID,
	)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to encode GetTransmissionState: %w", err)
	}

	// Call the view function via AptosService
	viewReply, err := fc.AptosService.View(ctx, aptostypes.ViewRequest{
		Payload: &aptostypes.ViewPayload{
			Module: aptostypes.ModuleID{
				Address: aptostypes.AccountAddress(moduleInfo.Address),
				Name:    moduleInfo.ModuleName,
			},
			Function: functionName,
			Args:     args,
		},
	})
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to call GetTransmissionState view: %w", err)
	}

	// Parse the JSON result — view returns a JSON array like [true] or [false]
	var result []bool
	if err := json.Unmarshal(viewReply.Data, &result); err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal transmission state: %w", err)
	}
	if len(result) != 1 {
		return TransmissionInfo{}, fmt.Errorf("unexpected transmission state result length: %d", len(result))
	}

	if !result[0] {
		return TransmissionInfo{Success: false}, nil
	}

	// Transmission exists, fetch transmitter too.
	// get_transmitter returns Option<address>, represented in JSON as {"vec": ["0x..."]} when present.
	moduleInfo, functionName, _, args, err = fc.forwarderEncoder.GetTransmitter(
		transmissionID.Receiver,
		transmissionID.WorkflowExecutionID[:],
		reportID,
	)
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to encode GetTransmitter: %w", err)
	}

	viewReply, err = fc.AptosService.View(ctx, aptostypes.ViewRequest{
		Payload: &aptostypes.ViewPayload{
			Module: aptostypes.ModuleID{
				Address: aptostypes.AccountAddress(moduleInfo.Address),
				Name:    moduleInfo.ModuleName,
			},
			Function: functionName,
			Args:     args,
		},
	})
	if err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to call GetTransmitter view: %w", err)
	}

	var txResult []struct {
		Vec []string `json:"vec"`
	}
	if err := json.Unmarshal(viewReply.Data, &txResult); err != nil {
		return TransmissionInfo{}, fmt.Errorf("failed to unmarshal transmitter result: %w", err)
	}
	if len(txResult) != 1 {
		return TransmissionInfo{}, fmt.Errorf("unexpected transmitter result length: %d", len(txResult))
	}

	transmitter := ""
	if len(txResult[0].Vec) > 0 {
		transmitter = txResult[0].Vec[0]
	}

	return TransmissionInfo{Success: true, Transmitter: transmitter}, nil
}

func (fc *forwarderClient) GetTransmissionTxHash(
	ctx context.Context,
	transmissionID TransmissionID,
	transmitter string,
	expectedForwarderRawReport []byte,
) (string, error) {
	if transmitter == "" {
		return "", fmt.Errorf("transmitter is empty")
	}

	txReader, ok := fc.AptosService.(accountTransactionsReader)
	if !ok {
		return "", fmt.Errorf("aptos client does not expose AccountTransactions")
	}

	var transmitterAddr aptos_sdk.AccountAddress
	if err := transmitterAddr.ParseStringRelaxed(transmitter); err != nil {
		return "", fmt.Errorf("invalid transmitter address %q: %w", transmitter, err)
	}
	var transmitterAddress aptostypes.AccountAddress
	copy(transmitterAddress[:], transmitterAddr[:])

	// Avoid Aptos SDK underflow bug in AccountTransactions(nil, limit>1) by first fetching
	// the latest tx to determine a safe explicit start sequence.
	latestLimit := uint64(1)
	latestTxs, err := txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
		Address: transmitterAddress,
		Limit:   &latestLimit,
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest account transaction: %w", err)
	}
	if len(latestTxs.Transactions) == 0 || latestTxs.Transactions[0] == nil {
		return "", fmt.Errorf("no account transactions found for transmitter %s", transmitter)
	}

	latestDecoded, err := decodeAccountUserTransaction(latestTxs.Transactions[0].Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode latest account transaction: %w", err)
	}
	initialUpperExclusive := latestDecoded.SequenceNumber + 1

	exactCandidates := make([]successfulTxCandidate, 0)
	fallbackCandidates := make([]successfulTxCandidate, 0)
	scanStats := successfulTxScanStats{}

	considerSuccessfulTx := func(tx *aptostypes.Transaction) {
		scanStats.transactionsScanned++
		if tx == nil {
			scanStats.nilTransactions++
			return
		}
		if tx.Success == nil || !*tx.Success {
			scanStats.nonSuccessfulTransactions++
			return
		}

		decoded, err := decodeAccountUserTransaction(tx.Data)
		if err != nil {
			scanStats.recordDecodeFailure(tx.Hash, err)
			return
		}

		if !isForwarderReportCall(decoded.EntryFunction, fc.forwarderAddress) {
			scanStats.nonForwarderReportCalls++
			return
		}
		if strings.TrimSpace(tx.Hash) == "" {
			scanStats.emptyHashCandidates++
			return
		}

		// Fallback candidate set is every successful forwarder::report call from
		// this transmitter; final selection is validated via TransactionByHash.
		addSuccessfulTxCandidate(&fallbackCandidates, tx.Hash, decoded.SequenceNumber)

		if !containsMatchingReportProcessed(decoded.Events, transmissionID) {
			scanStats.missingReportProcessed++
			return
		}

		addSuccessfulTxCandidate(&exactCandidates, tx.Hash, decoded.SequenceNumber)
	}

	processSuccessfulTxPage := func(txs []*aptostypes.Transaction) {
		scanStats.pagesScanned++
		for _, tx := range txs {
			considerSuccessfulTx(tx)
		}
	}

	scanRange := func(upperExclusive uint64, lowerBoundInclusive uint64) (uint64, error) {
		for upperExclusive > lowerBoundInclusive {
			limit := upperExclusive - lowerBoundInclusive
			if limit > maxAccountTransactionsPageSize {
				limit = maxAccountTransactionsPageSize
			}
			if limit == 0 {
				break
			}

			start := upperExclusive - limit
			txs, err := txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
				Address: transmitterAddress,
				Start:   &start,
				Limit:   &limit,
			})
			if err != nil {
				return upperExclusive, err
			}

			processSuccessfulTxPage(txs.Transactions)

			upperExclusive = start
			if len(exactCandidates) > 0 {
				break
			}
		}

		return upperExclusive, nil
	}

	// Aptos account transactions API has a per-request limit cap (currently 45),
	// so page backwards until the hardcoded lookback window is covered.
	fastLowerBound := uint64(0)
	if initialUpperExclusive > defaultTxHashLookback {
		fastLowerBound = initialUpperExclusive - defaultTxHashLookback
	}
	oldestScannedExclusive, err := scanRange(initialUpperExclusive, fastLowerBound)
	if err != nil {
		return "", fmt.Errorf("failed to fetch account transactions: %w", err)
	}

	// New transactions can be appended while paging through 45-sized batches.
	// If we haven't found a match yet, do one top-up pass over only the newly-added
	// sequence range so we don't miss transactions committed during the scan.
	if len(exactCandidates) == 0 {
		latestTxs, err = txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
			Address: transmitterAddress,
			Limit:   &latestLimit,
		})
		if err == nil && len(latestTxs.Transactions) > 0 && latestTxs.Transactions[0] != nil {
			latestDecoded2, decodeErr := decodeAccountUserTransaction(latestTxs.Transactions[0].Data)
			if decodeErr == nil {
				latestUpperExclusive := latestDecoded2.SequenceNumber + 1
				if latestUpperExclusive > initialUpperExclusive {
					if _, topUpErr := scanRange(latestUpperExclusive, initialUpperExclusive); topUpErr != nil && fc.lggr != nil {
						fc.lggr.Debugw("Top-up transaction scan failed", "transmitter", transmitter, "error", topUpErr)
					}
				}
			} else {
				scanStats.recordDecodeFailure(latestTxs.Transactions[0].Hash, decodeErr)
			}
		}
	}

	// If the fast lookback path yielded nothing, scan older history before giving up.
	if len(exactCandidates) == 0 && len(fallbackCandidates) == 0 && oldestScannedExclusive > 0 {
		if fc.lggr != nil {
			fc.lggr.Debugw(
				"Extending successful tx hash scan beyond default lookback",
				"transmitter", transmitter,
				"startSequenceExclusive", oldestScannedExclusive,
			)
		}
		if _, err := scanRange(oldestScannedExclusive, 0); err != nil {
			return "", fmt.Errorf("failed while extending account transactions scan: %w", err)
		}
	}

	sortSuccessfulTxCandidates(exactCandidates)
	sortSuccessfulTxCandidates(fallbackCandidates)

	selectCandidate := func(kind string, candidates []successfulTxCandidate) (string, bool, []string) {
		reasons := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			if len(expectedForwarderRawReport) == 0 {
				return candidate.Hash, true, reasons
			}
			validatedHash, validateErr := fc.validateSuccessfulTxHash(ctx, transmissionID, candidate.Hash, expectedForwarderRawReport)
			if validateErr == nil {
				return validatedHash, true, reasons
			}
			reasons = append(reasons, fmt.Sprintf("%s@seq=%d: %v", candidate.Hash, candidate.SequenceNumber, validateErr))
			if fc.lggr != nil {
				fc.lggr.Debugw(
					"Rejected successful tx hash candidate",
					"kind", kind,
					"hash", candidate.Hash,
					"sequenceNumber", candidate.SequenceNumber,
					"error", validateErr,
				)
			}
		}
		return "", false, reasons
	}

	exactHash, exactOK, exactReasons := selectCandidate("exact", exactCandidates)
	if exactOK {
		return exactHash, nil
	}
	fallbackHash, fallbackOK, fallbackReasons := selectCandidate("fallback", fallbackCandidates)
	if fallbackOK {
		return fallbackHash, nil
	}

	scanSummary := scanStats.summary()
	reasons := make([]string, 0, len(exactReasons)+len(fallbackReasons))
	reasons = append(reasons, exactReasons...)
	reasons = append(reasons, fallbackReasons...)
	if len(reasons) > 6 {
		reasons = reasons[:6]
	}
	if len(reasons) > 0 {
		return "", fmt.Errorf(
			"no matching successful report tx found for transmitter %s (candidate validation failures: %s; scan: %s)",
			transmitter,
			strings.Join(reasons, "; "),
			scanSummary,
		)
	}

	if len(exactCandidates) > 0 || len(fallbackCandidates) > 0 {
		return "", fmt.Errorf(
			"no matching successful report tx found for transmitter %s (exact_candidates=%d fallback_candidates=%d; scan: %s)",
			transmitter,
			len(exactCandidates),
			len(fallbackCandidates),
			scanSummary,
		)
	}
	return "", fmt.Errorf("no matching successful report tx found for transmitter %s (scan: %s)", transmitter, scanSummary)
}

func (fc *forwarderClient) ValidateFailedTxHash(ctx context.Context, transmissionID TransmissionID, txHash string, expectedForwarderRawReport []byte) (string, error) {
	return fc.validateTxHashByStatus(ctx, transmissionID, txHash, expectedForwarderRawReport, false)
}

func (fc *forwarderClient) validateSuccessfulTxHash(ctx context.Context, transmissionID TransmissionID, txHash string, expectedForwarderRawReport []byte) (string, error) {
	return fc.validateTxHashByStatus(ctx, transmissionID, txHash, expectedForwarderRawReport, true)
}

func (fc *forwarderClient) validateTxHashByStatus(
	ctx context.Context,
	transmissionID TransmissionID,
	txHash string,
	expectedForwarderRawReport []byte,
	expectSuccess bool,
) (string, error) {
	normalizedRequestedHash, ok := normalizeAptosTxHashString(txHash)
	if !ok {
		return "", fmt.Errorf("invalid tx hash %q", txHash)
	}

	reply, err := fc.AptosService.TransactionByHash(ctx, aptostypes.TransactionByHashRequest{Hash: normalizedRequestedHash})
	if err != nil {
		return "", fmt.Errorf("failed to fetch transaction by hash: %w", err)
	}
	if reply == nil || reply.Transaction == nil {
		return "", fmt.Errorf("transaction not found for hash %s", normalizedRequestedHash)
	}

	tx := reply.Transaction
	normalizedReturnedHash := normalizedRequestedHash
	if tx.Hash != "" {
		normalizedTxHash, txHashOK := normalizeAptosTxHashString(tx.Hash)
		if !txHashOK {
			return "", fmt.Errorf("transaction returned invalid hash format %q", tx.Hash)
		}
		normalizedReturnedHash = normalizedTxHash
	}
	if normalizedReturnedHash != normalizedRequestedHash {
		return "", fmt.Errorf("transaction hash mismatch: requested %s, returned %s", normalizedRequestedHash, normalizedReturnedHash)
	}

	if tx.Type == aptostypes.TransactionVariantPending {
		return "", fmt.Errorf("transaction %s is still pending", normalizedReturnedHash)
	}
	if tx.Type != aptostypes.TransactionVariantUser {
		return "", fmt.Errorf("transaction %s is not user_transaction: %s", normalizedReturnedHash, tx.Type)
	}
	if tx.Success == nil {
		return "", fmt.Errorf("transaction %s has unknown success status", normalizedReturnedHash)
	}
	if !expectSuccess && *tx.Success {
		return "", fmt.Errorf("transaction %s succeeded, expected failure", normalizedReturnedHash)
	}
	if expectSuccess && !*tx.Success {
		return "", fmt.Errorf("transaction %s failed, expected success", normalizedReturnedHash)
	}

	decoded, err := decodeAccountUserTransaction(tx.Data)
	if err != nil {
		return "", fmt.Errorf("failed to decode transaction %s: %w", normalizedReturnedHash, err)
	}
	if !isForwarderReportCall(decoded.EntryFunction, fc.forwarderAddress) {
		return "", fmt.Errorf("transaction %s is not a forwarder::report call", normalizedReturnedHash)
	}
	// For successful transmissions, different nodes can submit different raw_report bytes
	// (e.g. timestamp/signature set differences) for the same transmission ID. In that case,
	// accept a metadata-level payload match plus the matching ReportProcessed event.
	payloadMatchesExact := isMatchingForwarderReportPayload(decoded.PayloadArguments, transmissionID, expectedForwarderRawReport)
	payloadMatchesTransmission := payloadMatchesExact
	if expectSuccess {
		payloadMatchesTransmission = isMatchingForwarderReportPayload(decoded.PayloadArguments, transmissionID, nil)
	}
	if !payloadMatchesTransmission {
		return "", fmt.Errorf("transaction %s payload does not match requested transmission", normalizedReturnedHash)
	}

	if expectSuccess && !containsMatchingReportProcessed(decoded.Events, transmissionID) {
		return "", fmt.Errorf("transaction %s is missing matching ReportProcessed event", normalizedReturnedHash)
	}
	if expectSuccess && len(expectedForwarderRawReport) > 0 && !payloadMatchesExact && fc.lggr != nil {
		fc.lggr.Debugw(
			"Accepted successful tx hash candidate via metadata+event match despite raw report byte mismatch",
			"hash", normalizedReturnedHash,
		)
	}

	return normalizedReturnedHash, nil
}

func (fc *forwarderClient) GetTransmissionFailedTxHash(ctx context.Context, transmissionID TransmissionID, transmitters []string, maxLedgerVersion *uint64) (string, error) {
	txReader, ok := fc.AptosService.(accountTransactionsReader)
	if !ok {
		return "", fmt.Errorf("aptos client does not expose AccountTransactions")
	}

	orderedTransmitters := orderedUniqueTransmitters(transmitters)
	if len(orderedTransmitters) == 0 {
		return "", fmt.Errorf("no candidate transmitters provided")
	}

	var best *failedTxCandidate
	for i, transmitter := range orderedTransmitters {
		candidate, err := fc.findEarliestMatchingFailedTxForTransmitter(ctx, txReader, transmissionID, transmitter, i, maxLedgerVersion)
		if err != nil {
			fc.lggr.Debugw("Failed to resolve failed tx hash for transmitter", "transmitter", transmitter, "error", err)
			continue
		}
		if candidate == nil {
			continue
		}
		if best == nil || isEarlierFailedTx(*candidate, *best) {
			best = candidate
		}
	}

	if best == nil {
		return "", fmt.Errorf("no matching failed report tx found for candidate transmitters")
	}

	return best.Hash, nil
}

type successfulTxCandidate struct {
	Hash           string
	SequenceNumber uint64
}

func addSuccessfulTxCandidate(candidates *[]successfulTxCandidate, hash string, sequenceNumber uint64) {
	normalizedHash := strings.TrimSpace(hash)
	if normalizedHash == "" {
		return
	}

	for i := range *candidates {
		if (*candidates)[i].Hash != normalizedHash {
			continue
		}
		if sequenceNumber > (*candidates)[i].SequenceNumber {
			(*candidates)[i].SequenceNumber = sequenceNumber
		}
		return
	}

	*candidates = append(*candidates, successfulTxCandidate{
		Hash:           normalizedHash,
		SequenceNumber: sequenceNumber,
	})
}

func sortSuccessfulTxCandidates(candidates []successfulTxCandidate) {
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].SequenceNumber > candidates[j].SequenceNumber
	})
}

func orderedUniqueTransmitters(transmitters []string) []string {
	if len(transmitters) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(transmitters))
	out := make([]string, 0, len(transmitters))
	for _, transmitter := range transmitters {
		normalized := normalizeAptosHexAddress(transmitter)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

type failedTxCandidate struct {
	Hash             string
	TransmitterIndex int
	SequenceNumber   uint64
	Version          uint64
	TimestampMicros  uint64
	HasTimestamp     bool
}

func isEarlierFailedTx(candidate failedTxCandidate, current failedTxCandidate) bool {
	// Prefer earliest wall-clock failed tx timestamp when available.
	if candidate.HasTimestamp && current.HasTimestamp && candidate.TimestampMicros != current.TimestampMicros {
		return candidate.TimestampMicros < current.TimestampMicros
	}
	if candidate.HasTimestamp != current.HasTimestamp {
		return candidate.HasTimestamp
	}

	// If timestamps are equal/unavailable, fall back to submitter order.
	if candidate.TransmitterIndex != current.TransmitterIndex {
		return candidate.TransmitterIndex < current.TransmitterIndex
	}

	// Earlier sequence is an earlier attempt from the same transmitter.
	if candidate.SequenceNumber != current.SequenceNumber {
		return candidate.SequenceNumber < current.SequenceNumber
	}

	return candidate.Version < current.Version
}

func (fc *forwarderClient) findEarliestMatchingFailedTxForTransmitter(
	ctx context.Context,
	txReader accountTransactionsReader,
	transmissionID TransmissionID,
	transmitter string,
	transmitterIndex int,
	maxLedgerVersion *uint64,
) (*failedTxCandidate, error) {
	var transmitterAddr aptos_sdk.AccountAddress
	if err := transmitterAddr.ParseStringRelaxed(transmitter); err != nil {
		return nil, fmt.Errorf("invalid transmitter address %q: %w", transmitter, err)
	}
	var transmitterAddress aptostypes.AccountAddress
	copy(transmitterAddress[:], transmitterAddr[:])

	latestLimit := uint64(1)
	latestTxs, err := txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
		Address: transmitterAddress,
		Limit:   &latestLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch latest account transaction: %w", err)
	}
	if len(latestTxs.Transactions) == 0 || latestTxs.Transactions[0] == nil {
		return nil, nil
	}

	latestDecoded, err := decodeAccountUserTransaction(latestTxs.Transactions[0].Data)
	if err != nil {
		return nil, fmt.Errorf("failed to decode latest account transaction: %w", err)
	}
	initialUpperExclusive := latestDecoded.SequenceNumber + 1
	if initialUpperExclusive == 0 {
		return nil, nil
	}

	best := (*failedTxCandidate)(nil)
	remaining := defaultTxHashLookback
	if initialUpperExclusive < remaining {
		remaining = initialUpperExclusive
	}
	upperExclusive := initialUpperExclusive

	scanWindow := func(start uint64, limit uint64) error {
		txs, err := txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
			Address: transmitterAddress,
			Start:   &start,
			Limit:   &limit,
		})
		if err != nil {
			return err
		}

		for _, tx := range txs.Transactions {
			if tx == nil || tx.Success == nil || *tx.Success {
				continue
			}

			decoded, err := decodeAccountUserTransaction(tx.Data)
			if err != nil {
				continue
			}
			if maxLedgerVersion != nil && decoded.Version > *maxLedgerVersion {
				continue
			}
			if !matchesForwarderReportCallAndPayload(decoded, fc.forwarderAddress, transmissionID, nil) {
				continue
			}
			if tx.Hash == "" {
				continue
			}

			candidate := failedTxCandidate{
				Hash:             tx.Hash,
				TransmitterIndex: transmitterIndex,
				SequenceNumber:   decoded.SequenceNumber,
				Version:          decoded.Version,
				TimestampMicros:  decoded.TimestampMicros,
				HasTimestamp:     decoded.HasTimestamp,
			}
			if best == nil || isEarlierFailedTx(candidate, *best) {
				best = &candidate
			}
		}
		return nil
	}

	for remaining > 0 {
		limit := remaining
		if limit > maxAccountTransactionsPageSize {
			limit = maxAccountTransactionsPageSize
		}
		if upperExclusive < limit {
			limit = upperExclusive
		}
		if limit == 0 {
			break
		}

		start := upperExclusive - limit
		if err := scanWindow(start, limit); err != nil {
			return nil, fmt.Errorf("failed to fetch account transactions: %w", err)
		}
		if start == 0 {
			break
		}
		upperExclusive = start
		remaining -= limit
	}

	// New transactions can be appended while paging through 45-sized batches.
	// If we haven't matched yet, run one top-up pass over the newly-added range.
	if best == nil && maxLedgerVersion == nil {
		latestTxs, err = txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
			Address: transmitterAddress,
			Limit:   &latestLimit,
		})
		if err == nil && len(latestTxs.Transactions) > 0 && latestTxs.Transactions[0] != nil {
			latestDecoded2, decodeErr := decodeAccountUserTransaction(latestTxs.Transactions[0].Data)
			if decodeErr == nil {
				latestUpperExclusive := latestDecoded2.SequenceNumber + 1
				if latestUpperExclusive > initialUpperExclusive {
					upperExclusive = latestUpperExclusive
					remaining = latestUpperExclusive - initialUpperExclusive
					for remaining > 0 {
						limit := remaining
						if limit > maxAccountTransactionsPageSize {
							limit = maxAccountTransactionsPageSize
						}
						if upperExclusive-limit < initialUpperExclusive {
							limit = upperExclusive - initialUpperExclusive
						}
						if limit == 0 {
							break
						}

						start := upperExclusive - limit
						if err := scanWindow(start, limit); err != nil {
							break
						}

						upperExclusive = start
						remaining -= limit
						if upperExclusive == initialUpperExclusive {
							break
						}
					}
				}
			}
		}
	}

	return best, nil
}

type accountTxEvent struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type accountUserTransaction struct {
	EntryFunction    string           `json:"entry_function"`
	SequenceNumber   uint64           `json:"sequence_number"`
	Version          uint64           `json:"version"`
	TimestampMicros  uint64           `json:"timestamp_micros"`
	HasTimestamp     bool             `json:"has_timestamp"`
	PayloadArguments []any            `json:"payload_arguments"`
	Events           []accountTxEvent `json:"events"`
}

func decodeAccountUserTransaction(raw []byte) (*accountUserTransaction, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty transaction payload")
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		return nil, err
	}

	txBody := body
	if inner, ok := getMapField(body, "inner", "Inner"); ok {
		txBody = inner
	}

	txType, _ := getStringField(body, "type", "Type")
	if txType == "" {
		txType, _ = getStringField(txBody, "type", "Type")
	}
	if txType != "user_transaction" {
		return nil, fmt.Errorf("transaction type %q is not user_transaction", txType)
	}

	sequenceNumber, err := parseFirstUint64Field(txBody, body, "sequence_number", "SequenceNumber")
	if err != nil {
		return nil, fmt.Errorf("failed to parse sequence_number: %w", err)
	}

	entryFunction := ""
	payloadArguments := make([]any, 0)
	if payload, ok := getMapField(txBody, "payload", "Payload"); ok {
		if fn, ok := getStringField(payload, "function", "Function"); ok {
			entryFunction = fn
			if args, ok := getSliceField(payload, "arguments", "Arguments"); ok {
				payloadArguments = args
			}
		} else if innerPayload, ok := getMapField(payload, "inner", "Inner"); ok {
			if fn, ok := getStringField(innerPayload, "function", "Function"); ok {
				entryFunction = fn
			}
			if args, ok := getSliceField(innerPayload, "arguments", "Arguments"); ok {
				payloadArguments = args
			}
		}
	}

	version, _ := parseFirstOptionalUint64Field(txBody, body, "version", "Version")
	timestampMicros, hasTimestamp := parseFirstOptionalUint64Field(
		txBody,
		body,
		"timestamp",
		"Timestamp",
		"timestamp_usecs",
		"timestamp_micros",
		"TimestampUsecs",
		"TimestampMicros",
	)

	events := make([]accountTxEvent, 0)
	if rawEvents, ok := getSliceField(txBody, "events", "Events"); ok {
		for _, rawEvent := range rawEvents {
			eventMap, ok := rawEvent.(map[string]any)
			if !ok {
				continue
			}
			eventType, _ := getStringField(eventMap, "type", "Type")
			eventData, _ := getMapField(eventMap, "data", "Data")
			events = append(events, accountTxEvent{
				Type: eventType,
				Data: eventData,
			})
		}
	}

	return &accountUserTransaction{
		EntryFunction:    entryFunction,
		SequenceNumber:   sequenceNumber,
		Version:          version,
		TimestampMicros:  timestampMicros,
		HasTimestamp:     hasTimestamp,
		PayloadArguments: payloadArguments,
		Events:           events,
	}, nil
}

func parseFirstUint64Field(primary map[string]any, secondary map[string]any, keys ...string) (uint64, error) {
	if raw, ok := getField(primary, keys...); ok {
		value, err := parseUint64Value(raw)
		if err == nil {
			return value, nil
		}
	}
	if raw, ok := getField(secondary, keys...); ok {
		return parseUint64Value(raw)
	}
	return 0, fmt.Errorf("missing field %q", keys[0])
}

func parseFirstOptionalUint64Field(primary map[string]any, secondary map[string]any, keys ...string) (uint64, bool) {
	if raw, ok := getField(primary, keys...); ok {
		value, err := parseUint64Value(raw)
		if err == nil {
			return value, true
		}
	}
	if raw, ok := getField(secondary, keys...); ok {
		value, err := parseUint64Value(raw)
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

func getField(m map[string]any, keys ...string) (any, bool) {
	for _, k := range keys {
		v, ok := m[k]
		if ok {
			return v, true
		}
	}
	return nil, false
}

func getStringField(m map[string]any, keys ...string) (string, bool) {
	v, ok := getField(m, keys...)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}

func getMapField(m map[string]any, keys ...string) (map[string]any, bool) {
	v, ok := getField(m, keys...)
	if !ok {
		return nil, false
	}
	mv, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return mv, true
}

func getSliceField(m map[string]any, keys ...string) ([]any, bool) {
	v, ok := getField(m, keys...)
	if !ok {
		return nil, false
	}
	s, ok := v.([]any)
	if !ok {
		return nil, false
	}
	return s, true
}

func parseUint64Value(v any) (uint64, error) {
	switch x := v.(type) {
	case string:
		if x == "" {
			return 0, fmt.Errorf("empty string")
		}
		return strconv.ParseUint(x, 10, 64)
	case json.Number:
		return strconv.ParseUint(x.String(), 10, 64)
	case float64:
		if x < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(x), nil
	case int:
		if x < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(x), nil
	case int64:
		if x < 0 {
			return 0, fmt.Errorf("negative value")
		}
		return uint64(x), nil
	case uint64:
		return x, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", v)
	}
}

func isForwarderReportCall(entryFunction string, forwarderAddr [32]byte) bool {
	if entryFunction == "" {
		return false
	}
	if !strings.HasSuffix(entryFunction, "::forwarder::report") {
		return false
	}

	parts := strings.SplitN(entryFunction, "::", 2)
	if len(parts) < 2 {
		return false
	}
	fnAddress := normalizeAptosHexAddress(parts[0])
	forwarderAccount := aptos_sdk.AccountAddress(forwarderAddr)
	forwarderAddress := normalizeAptosHexAddress(forwarderAccount.StringLong())
	return fnAddress == forwarderAddress
}

func matchesForwarderReportCallAndPayload(
	decoded *accountUserTransaction,
	forwarderAddr [32]byte,
	transmissionID TransmissionID,
	expectedForwarderRawReport []byte,
) bool {
	if decoded == nil {
		return false
	}
	if !isForwarderReportCall(decoded.EntryFunction, forwarderAddr) {
		return false
	}
	return isMatchingForwarderReportPayload(decoded.PayloadArguments, transmissionID, expectedForwarderRawReport)
}

func containsMatchingReportProcessed(events []accountTxEvent, transmissionID TransmissionID) bool {
	for _, event := range events {
		if !strings.HasSuffix(strings.ToLower(event.Type), "::forwarder::reportprocessed") {
			continue
		}
		if isMatchingReportProcessedData(event.Data, transmissionID) {
			return true
		}
	}
	return false
}

func containsReportProcessedEvent(events []accountTxEvent) bool {
	for _, event := range events {
		if strings.HasSuffix(strings.ToLower(event.Type), "::forwarder::reportprocessed") {
			return true
		}
	}
	return false
}

func isMatchingReportProcessedData(data map[string]any, transmissionID TransmissionID) bool {
	if len(data) == 0 {
		return false
	}

	receiverStr, ok := getStringField(data, "receiver", "Receiver")
	if !ok {
		return false
	}
	var receiverAddr aptos_sdk.AccountAddress
	if err := receiverAddr.ParseStringRelaxed(receiverStr); err != nil {
		return false
	}
	if receiverAddr != transmissionID.Receiver {
		return false
	}

	reportIDRaw, ok := getField(data, "report_id", "reportId", "ReportId")
	if !ok {
		return false
	}
	reportID, ok := parseUint16(reportIDRaw)
	if !ok || reportID != binary.BigEndian.Uint16(transmissionID.ReportID[:]) {
		return false
	}

	execIDRaw, ok := getField(data, "workflow_execution_id", "workflowExecutionId", "WorkflowExecutionId")
	if !ok {
		return false
	}
	execID, ok := parseHexBytes(execIDRaw)
	if !ok {
		return false
	}
	return len(execID) == len(transmissionID.WorkflowExecutionID) &&
		string(execID) == string(transmissionID.WorkflowExecutionID[:])
}

func isMatchingForwarderReportPayload(payloadArgs []any, transmissionID TransmissionID, expectedForwarderRawReport []byte) bool {
	// forwarder::report(receiver, raw_report, signatures)
	if len(payloadArgs) < 2 {
		return false
	}

	receiver, ok := parseAccountAddress(payloadArgs[0])
	if !ok || receiver != transmissionID.Receiver {
		return false
	}

	rawReport, ok := parseBytes(payloadArgs[1])
	if !ok || len(rawReport) == 0 {
		return false
	}
	if len(expectedForwarderRawReport) > 0 {
		// Exact raw report byte equality is the strongest check; metadata is already
		// part of the report bytes and was validated upstream when report was built.
		return bytes.Equal(rawReport, expectedForwarderRawReport)
	}

	if isMatchingReportMetadata(rawReport, transmissionID) {
		return true
	}

	// Forwarder payload may be prefixed with report_context (96 bytes).
	if len(rawReport) > 96 && isMatchingReportMetadata(rawReport[96:], transmissionID) {
		return true
	}

	return false
}

func isMatchingReportMetadata(rawReport []byte, transmissionID TransmissionID) bool {
	metadata, err := decodeReportMetadata(rawReport)
	if err != nil {
		return false
	}
	if !strings.EqualFold(metadata.ExecutionID, hex.EncodeToString(transmissionID.WorkflowExecutionID[:])) {
		return false
	}
	reportID, ok := parseHexBytes(metadata.ReportID)
	if !ok || len(reportID) != 2 {
		return false
	}

	return reportID[0] == transmissionID.ReportID[0] && reportID[1] == transmissionID.ReportID[1]
}

func parseAccountAddress(v any) (aptos_sdk.AccountAddress, bool) {
	switch t := v.(type) {
	case string:
		var address aptos_sdk.AccountAddress
		if err := address.ParseStringRelaxed(t); err != nil {
			return aptos_sdk.AccountAddress{}, false
		}
		return address, true
	case map[string]any:
		if inner, ok := getField(t, "inner", "Inner", "value", "Value"); ok {
			return parseAccountAddress(inner)
		}
	}
	return aptos_sdk.AccountAddress{}, false
}

func parseUint16(v any) (uint16, bool) {
	switch t := v.(type) {
	case string:
		if strings.HasPrefix(strings.ToLower(t), "0x") {
			u, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(t), "0x"), 16, 16)
			if err != nil {
				return 0, false
			}
			return uint16(u), true
		}
		u, err := strconv.ParseUint(t, 10, 16)
		if err != nil {
			return 0, false
		}
		return uint16(u), true
	case json.Number:
		u, err := strconv.ParseUint(t.String(), 10, 16)
		if err != nil {
			return 0, false
		}
		return uint16(u), true
	case float64:
		if t < 0 || t > 65535 {
			return 0, false
		}
		return uint16(t), true
	case int:
		if t < 0 || t > 65535 {
			return 0, false
		}
		return uint16(t), true
	case int64:
		if t < 0 || t > 65535 {
			return 0, false
		}
		return uint16(t), true
	case uint64:
		if t > 65535 {
			return 0, false
		}
		return uint16(t), true
	default:
		return 0, false
	}
}

func parseHexBytes(v any) ([]byte, bool) {
	b, ok := parseBytes(v)
	if !ok {
		return nil, false
	}
	return b, true
}

func parseBytes(v any) ([]byte, bool) {
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		trimmedHex := strings.TrimPrefix(strings.ToLower(s), "0x")
		if len(trimmedHex)%2 != 0 {
			trimmedHex = "0" + trimmedHex
		}
		if trimmedHex != "" {
			if b, err := hexToBytes(trimmedHex); err == nil {
				return b, true
			}
		}
		if b, err := base64.StdEncoding.DecodeString(s); err == nil {
			return b, true
		}
		return nil, false
	case []byte:
		return t, true
	case []any:
		out := make([]byte, 0, len(t))
		for _, item := range t {
			u, err := parseUint64Value(item)
			if err != nil || u > 255 {
				return nil, false
			}
			out = append(out, byte(u))
		}
		return out, true
	case map[string]any:
		if inner, ok := getField(t, "inner", "Inner", "value", "Value"); ok {
			return parseBytes(inner)
		}
		return nil, false
	default:
		return nil, false
	}
}

func hexToBytes(s string) ([]byte, error) {
	return hex.DecodeString(s)
}
