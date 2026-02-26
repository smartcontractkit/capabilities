package actions

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	GetTransmissionTxHash(ctx context.Context, transmissionID TransmissionID, transmitter string) (string, error)
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

func (fc *forwarderClient) GetTransmissionTxHash(ctx context.Context, transmissionID TransmissionID, transmitter string) (string, error) {
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

	bestSeq := uint64(0)
	bestHash := ""
	remaining := defaultTxHashLookback
	if initialUpperExclusive < remaining {
		remaining = initialUpperExclusive
	}

	// Aptos account transactions API has a per-request limit cap (currently 45),
	// so page backwards until the hardcoded lookback window is covered.
	upperExclusive := initialUpperExclusive
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
		txs, err := txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
			Address: transmitterAddress,
			Start:   &start,
			Limit:   &limit,
		})
		if err != nil {
			return "", fmt.Errorf("failed to fetch account transactions: %w", err)
		}

		// Pick the matching tx with highest sequence number (latest successful matching tx).
		for _, tx := range txs.Transactions {
			if tx == nil || tx.Success == nil || !*tx.Success {
				continue
			}

			decoded, err := decodeAccountUserTransaction(tx.Data)
			if err != nil {
				continue
			}

			if !isForwarderReportCall(decoded.EntryFunction, fc.forwarderAddress) {
				continue
			}

			if !containsMatchingReportProcessed(decoded.Events, transmissionID) {
				continue
			}

			if bestHash == "" || decoded.SequenceNumber > bestSeq {
				bestSeq = decoded.SequenceNumber
				bestHash = tx.Hash
			}
		}

		if start == 0 {
			break
		}
		upperExclusive = start
		remaining -= limit
	}

	// New transactions can be appended while paging through 45-sized batches.
	// If we haven't found a match yet, do one top-up pass over only the newly-added
	// sequence range so we don't miss transactions committed during the scan.
	if bestHash == "" {
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
						txs, err := txReader.AccountTransactions(ctx, aptostypes.AccountTransactionsRequest{
							Address: transmitterAddress,
							Start:   &start,
							Limit:   &limit,
						})
						if err != nil {
							break
						}

						for _, tx := range txs.Transactions {
							if tx == nil || tx.Success == nil || !*tx.Success {
								continue
							}
							decoded, err := decodeAccountUserTransaction(tx.Data)
							if err != nil {
								continue
							}
							if !isForwarderReportCall(decoded.EntryFunction, fc.forwarderAddress) {
								continue
							}
							if !containsMatchingReportProcessed(decoded.Events, transmissionID) {
								continue
							}
							if bestHash == "" || decoded.SequenceNumber > bestSeq {
								bestSeq = decoded.SequenceNumber
								bestHash = tx.Hash
							}
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

	if bestHash != "" {
		return bestHash, nil
	}

	return "", fmt.Errorf("no matching successful report tx found for transmitter %s", transmitter)
}

type accountTxEvent struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

type accountUserTransaction struct {
	EntryFunction  string           `json:"entry_function"`
	SequenceNumber uint64           `json:"sequence_number"`
	Events         []accountTxEvent `json:"events"`
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

	sequenceRaw, ok := getField(txBody, "sequence_number", "SequenceNumber")
	if !ok {
		return nil, fmt.Errorf("missing sequence_number field")
	}
	sequenceNumber, err := parseUint64Value(sequenceRaw)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sequence_number: %w", err)
	}

	entryFunction := ""
	if payload, ok := getMapField(txBody, "payload", "Payload"); ok {
		if fn, ok := getStringField(payload, "function", "Function"); ok {
			entryFunction = fn
		} else if innerPayload, ok := getMapField(payload, "inner", "Inner"); ok {
			if fn, ok := getStringField(innerPayload, "function", "Function"); ok {
				entryFunction = fn
			}
		}
	}

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
		EntryFunction:  entryFunction,
		SequenceNumber: sequenceNumber,
		Events:         events,
	}, nil
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

func isMatchingReportProcessedData(data map[string]any, transmissionID TransmissionID) bool {
	if len(data) == 0 {
		return false
	}

	receiverStr, ok := data["receiver"].(string)
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

	reportID, ok := parseUint16(data["report_id"])
	if !ok || reportID != binary.BigEndian.Uint16(transmissionID.ReportID[:]) {
		return false
	}

	execIDRaw, ok := data["workflow_execution_id"]
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

func parseUint16(v any) (uint16, bool) {
	switch t := v.(type) {
	case string:
		u, err := strconv.ParseUint(t, 10, 16)
		if err != nil {
			return 0, false
		}
		return uint16(u), true
	case float64:
		if t < 0 || t > 65535 {
			return 0, false
		}
		return uint16(t), true
	default:
		return 0, false
	}
}

func parseHexBytes(v any) ([]byte, bool) {
	s, ok := v.(string)
	if !ok {
		return nil, false
	}
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	if len(s)%2 != 0 {
		s = "0" + s
	}
	b, err := hexToBytes(s)
	if err != nil {
		return nil, false
	}
	return b, true
}

func hexToBytes(s string) ([]byte, error) {
	return hex.DecodeString(s)
}
