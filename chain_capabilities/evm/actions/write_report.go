package actions

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/jpillora/backoff"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/retry"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
)

const (
	TransmissionStateNotAttempted uint8 = iota
	TransmissionStateSucceeded
	TransmissionStateInvalidReceiver
	TransmissionStateFailed
)

const UnknownIssueExecutingReceiverContractMessage = "unknown issue execution receiver contract"
const userError = "user error:"

// ErrUnexpectedSuccessfulTransmission indicates we expected a failed transmission but found a successful one
var ErrUnexpectedSuccessfulTransmission = errors.New("unexpected successful transmission")

// withQuickRetry wraps a simple RPC read with retry logic.
// Uses shorter timeout (10s) and fast backoff - these calls should be sub-second.
func withQuickRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error)) (T, error) {
	return withRetry(ctx, lggr, fn, 10*time.Second, 1*time.Second, 10)
}

// withPollingRetry wraps an operation that polls for state changes.
// Uses longer timeout (60s) to accommodate slow chains.
func withPollingRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error)) (T, error) {
	return withRetry(ctx, lggr, fn, 60*time.Second, 3*time.Second, 25)
}

// withRetry executes fn with exponential backoff retry logic.
// Returns the original error from fn, not the retry wrapper error.
func withRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error), timeout, maxBackoff time.Duration, maxRetries uint) (T, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	strategy := retry.Strategy[T]{
		Backoff:    &backoff.Backoff{Factor: 2, Min: 100 * time.Millisecond, Max: maxBackoff},
		MaxRetries: maxRetries,
	}
	result, err := strategy.Do(ctx, lggr, func(ctx context.Context) (T, error) {
		r, e := fn(ctx)
		if e != nil {
			lastErr = e // Capture the original error from fn
		}
		return r, e
	})
	if err != nil {
		if lastErr != nil {
			return result, lastErr
		}
		// lastErr is nil - fn was never called, return retry error
		return result, err
	}
	return result, nil
}

func decodeReportMetadata(data []byte) (ocrtypes.Metadata, error) {
	metadata, _, err := ocrtypes.Decode(data)
	return metadata, err
}

type WriteReport struct {
	types.EVMService
	forwarderClient    contracts.CREForwarderClient
	ReceiverGasMinimum uint64
	chainSelector      uint64

	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder

	txGasLimit      limits.BoundLimiter[uint64]
	reportSizeLimit limits.BoundLimiter[commoncfg.Size]
}

func (e *EVM) WriteReport(ctx context.Context, metadata capabilities.RequestMetadata, input *evm.WriteReportRequest) (*capabilities.ResponseAndMetadata[*evm.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportInitiated(telemetryContext, input))
	if err := e.validateInputsAndReportMetadata(metadata, input); err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport, user error due to invalid request", err.Error(), true))
		return nil, NewUserError(err)
	}

	report, billingMetadata, err := e.executeWriteReport(ctx, input, metadata, telemetryContext)
	if err != nil {
		isUserError := e.isUserErrorWriteReport(err)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor,
			e.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport while checking if the report exists or trying to publish on chain", err.Error(), isUserError))
		return nil, GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully executed WriteReport", e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportSuccess(telemetryContext, input))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.WriteReportReply]{
		Response:         report,
		ResponseMetadata: billingMetadata,
	}
	return &responseAndMetadata, nil
}

func (e *EVM) executeWriteReport(ctx context.Context, request *evm.WriteReportRequest, metadata capabilities.RequestMetadata, telemetryContext monitoring.TelemetryContext) (*evm.WriteReportReply, capabilities.ResponseMetadata, error) {
	wr := &WriteReport{
		EVMService:         e.EVMService,
		forwarderClient:    e.forwarderClient,
		ReceiverGasMinimum: e.ReceiverGasMinimum,
		chainSelector:      e.chainSelector,

		lggr:              e.messageBuilder.RequestLggr(e.lggr, telemetryContext),
		beholderProcessor: e.beholderProcessor,
		messageBuilder:    e.messageBuilder,

		txGasLimit:      e.txGasLimit,
		reportSizeLimit: e.reportSizeLimit,
	}

	return wr.executeWriteReport(ctx, request, metadata, telemetryContext)
}

func (e *WriteReport) getFee(ctx context.Context, txIdempotencyKey string) (*big.Float, error) {
	if txIdempotencyKey == "" {
		return nil, fmt.Errorf("txIdempotencyKey is empty, cannot retrieve transaction fee")
	}

	feeInWei, errTxFee := e.GetTransactionFee(ctx, txIdempotencyKey)
	if errTxFee != nil {
		return nil, fmt.Errorf("failed to get transaction fee: %w", errTxFee)
	}
	feeInEth := new(big.Float).Quo(new(big.Float).SetInt(feeInWei.TransactionFee), big.NewFloat(1e18))
	e.lggr.Debugw("WriteReport fee", "feeInEth", feeInEth.String(), "feeInWei", feeInWei.TransactionFee.String())
	return feeInEth, nil
}

func (e *WriteReport) executeWriteReport(ctx context.Context, request *evm.WriteReportRequest, metadata capabilities.RequestMetadata, telemetryContext monitoring.TelemetryContext) (*evm.WriteReportReply, capabilities.ResponseMetadata, error) {
	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	ctx = contexts.WithChainSelector(ctx, e.chainSelector)
	if request.GasConfig == nil || request.GasConfig.GasLimit == 0 {
		request.GasConfig = &evm.GasConfig{}
		request.GasConfig.GasLimit, err = e.txGasLimit.Limit(ctx)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, err
		}
	} else {
		err = e.txGasLimit.Check(ctx, request.GasConfig.GasLimit)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s gas limit exceeds configured limit (gasLimit=%d): %w", userError, request.GasConfig.GasLimit, err)
		}
	}

	var transmissionInfo contracts.TransmissionInfo
	transmissionInfo, err = withQuickRetry(ctx, e.lggr, func(ctx context.Context) (contracts.TransmissionInfo, error) {
		return e.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
	})
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get transmission info: %w", err)
	}

	e.lggr.Infow("Checking transmission status", transmissionInfo.LogAttrs()...)

	txHashRetriever := NewTxHashRetriever(e.forwarderClient, e.lggr, transmissionID)
	switch transmissionInfo.State {
	case TransmissionStateNotAttempted:
		e.lggr.Infow("transmission not attempted - attempting to push to txmgr")
	case TransmissionStateSucceeded:
		txHash, err := txHashRetriever.GetSuccessfulTransmissionHash(ctx)
		if err != nil {
			e.lggr.Errorw("Returning without a transmission attempt - report already onchain, but failed to retrieve its txHash", "error", err.Error())
			return nil, capabilities.ResponseMetadata{}, err
		}

		e.lggr.Infow("Returning without a transmission attempt - report already onchain", "txHash", common.Bytes2Hex(txHash[:]))
		reply, err := e.fetchTransactionReceiptAndCreateReply(ctx, *txHash, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS, nil)
		return reply, capabilities.ResponseMetadata{}, err
	case TransmissionStateInvalidReceiver:
		txHash, err := txHashRetriever.GetFailedTransmissionHash(ctx)
		if err != nil {
			if errors.Is(err, ErrUnexpectedSuccessfulTransmission) {
				monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportInvalidTransmissionState(telemetryContext, request, transmissionInfo, "WriteReport unexpected successful transmission", err.Error()))
			} else {
				e.lggr.Errorw("Transmission already done by another node but failed due to invalid receiver, not reattempting and failed to get its txHash")
			}
			return nil, capabilities.ResponseMetadata{}, err
		}

		e.lggr.Infow("Transmission already done by another node but failed due to invalid receiver, not reattempting", "txHash", common.Bytes2Hex(txHash[:]))
		reply, err := e.processUnrecoverableTxState(ctx, request, *txHash, transmissionInfo, transmissionID, false)
		return reply, capabilities.ResponseMetadata{}, err
	case TransmissionStateFailed:
		txGasLimit := e.ReceiverGasMinimum + contracts.ForwarderContractLogicGasCost
		if request.GasConfig != nil && request.GasConfig.GasLimit > txGasLimit {
			txGasLimit = request.GasConfig.GasLimit - contracts.ForwarderContractLogicGasCost
		}
		if transmissionInfo.GasLimit.Uint64() > txGasLimit {
			txHash, err := txHashRetriever.GetFailedTransmissionHash(ctx)
			if err != nil {
				if errors.Is(err, ErrUnexpectedSuccessfulTransmission) {
					monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportInvalidTransmissionState(telemetryContext, request, transmissionInfo, "WriteReport unexpected successful transmission", err.Error()))
				} else {
					e.lggr.Errorw("Returning without a transmission attempt - transmission already attempted, but failed to retrieve its tx hash", "error", err.Error(), "txGasLimit", txGasLimit, "transmissionGasLimit", transmissionInfo.GasLimit)
				}
				return nil, capabilities.ResponseMetadata{}, err
			}

			e.lggr.Infow("Returning without a transmission attempt - transmission already attempted and failed", "transmissionTxHash", common.Bytes2Hex(txHash[:]), "txGasLimit", txGasLimit, "transmissionGasLimit", transmissionInfo.GasLimit)
			reply, err := e.fetchTransactionReceiptAndCreateReply(ctx, *txHash, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, nil)
			return reply, capabilities.ResponseMetadata{}, err
		}
		e.lggr.Infow("Retrying a failed transmission - attempting to push to txmgr", "txGasLimit", txGasLimit, "transmissionGasLimit", transmissionInfo.GasLimit)
	default:
		errorMsg := getInvalidStateErrorMessage(transmissionInfo.State)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportInvalidTransmissionState(telemetryContext, request, transmissionInfo, "WriteReport invalid transmission state", errorMsg))
		return nil, capabilities.ResponseMetadata{}, errors.New(errorMsg)
	}

	err = e.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s report size exceeds limit: %w", userError, err)
	}

	e.lggr.Debugw("Submitting transaction")
	transactionResult, err := e.forwarderClient.InvokeOnReport(ctx, transmissionID.Receiver, request.Report, request.GasConfig)
	if err != nil {
		e.lggr.Errorw("Transaction failed", "error", err.Error())
		return nil, capabilities.ResponseMetadata{}, err
	}

	transmissionInfo, err = withPollingRetry(ctx, e.lggr, func(ctx context.Context) (contracts.TransmissionInfo, error) {
		readTransmissionInfo, readTransmissionErr := e.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if readTransmissionErr != nil {
			return contracts.TransmissionInfo{}, readTransmissionErr
		}
		if readTransmissionInfo.State != TransmissionStateNotAttempted {
			return readTransmissionInfo, nil
		}
		return contracts.TransmissionInfo{}, errors.New("transaction successfully executed but not yet seeing the transmission info updated, retrying getting transmission info")
	})

	if err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed getting transmission info after node submitted the report on chain, %w", err)
	}

	e.lggr.Infow("Got final transmission status", transmissionInfo.LogAttrs()...)

	var meteringMetadata capabilities.ResponseMetadata
	transactionFee, err := e.getFee(ctx, transactionResult.TxIdempotencyKey)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportTxFeeCalculationError(telemetryContext, request, transactionResult.TxIdempotencyKey, err.Error()))
	} else {
		meteringMetadata = metering.GetResponseMetadataWriteReport(transactionFee, e.chainSelector)
	}

	switch transmissionInfo.State {
	case TransmissionStateSucceeded:
		txHash := &transactionResult.TxHash
		if transactionResult.TxStatus == evmtypes.TxReverted {
			// Report for this transaction has already been submitted and we sent a duplicate tx onchain which is fine, but wastes ethereum gas
			txHash, err = txHashRetriever.GetSuccessfulTransmissionHash(ctx)
			if err != nil {
				return nil, capabilities.ResponseMetadata{}, err
			}
			monitoring.LogAndEmitSuccess(ctx, "writeReport sent a duplicate transaction - report already submitted", e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportDuplicateTx(telemetryContext, request, common.Bytes2Hex(transactionResult.TxHash[:]), common.Bytes2Hex((*txHash)[:])))
		}
		e.lggr.Debugw("transaction confirmed", "executionID", metadata.WorkflowExecutionID, "txIdempotencyKey", transactionResult.TxIdempotencyKey, "txHash", common.Bytes2Hex((*txHash)[:]))
		reply, err := e.fetchTransactionReceiptAndCreateReply(ctx, *txHash, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS, nil)
		return reply, meteringMetadata, err
	case TransmissionStateFailed, TransmissionStateInvalidReceiver:
		reply, err := e.processUnrecoverableTxState(ctx, request, transactionResult.TxHash, transmissionInfo, transmissionID, true)
		return reply, meteringMetadata, err
	default:
		errorMsg := getInvalidStateErrorMessage(transmissionInfo.State)
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportInvalidTransmissionState(telemetryContext, request, transmissionInfo, "WriteReport invalid transmission state", errorMsg))
		return nil, meteringMetadata, errors.New(errorMsg)
	}
}

func getInvalidStateErrorMessage(state uint8) string {
	return fmt.Sprintf("unexpected transmission state: %v", state)
}

func (e *WriteReport) processUnrecoverableTxState(ctx context.Context, request *evm.WriteReportRequest, txHash evmtypes.Hash, transmissionInfo contracts.TransmissionInfo, transmissionID contracts.TransmissionID, txAttemptedLocally bool) (*evm.WriteReportReply, error) {
	var message *string
	if transmissionInfo.State == TransmissionStateInvalidReceiver {
		message = getInvalidReceiverMessage(transmissionID.Receiver[:])
	} else {
		message = ptr(UnknownIssueExecutingReceiverContractMessage)
	}

	if !txAttemptedLocally {
		e.lggr.Infow("Returning without a transmission attempt - transmission already attempted, receiver was marked as invalid", "message", message)
	} else {
		e.lggr.Errorw("Transaction written to the forwarder, but failed to be written to the consumer contract", "receiver", common.Bytes2Hex(request.Receiver), "message", message, "transmissionState", transmissionInfo.State)
	}

	return e.fetchTransactionReceiptAndCreateReply(ctx, txHash, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, message)
}

func getInvalidReceiverMessage(receiver []byte) *string {
	return ptr(fmt.Sprintf("Invalid receiver: %s", common.Bytes2Hex(receiver)))
}

func getTransmissionID(workflowExecutionID string, request *evm.WriteReportRequest) (contracts.TransmissionID, error) {
	rawExecutionID, err := hex.DecodeString(workflowExecutionID)
	if err != nil {
		return contracts.TransmissionID{}, err
	}

	if len(rawExecutionID) != 32 {
		return contracts.TransmissionID{}, fmt.Errorf("workflowExecutionID must be 32 bytes, got %d", len(rawExecutionID))
	}

	reportMetadata, err := decodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return contracts.TransmissionID{}, fmt.Errorf("%s failed to decode report metadata: %v", userError, err)
	}

	reportID := common.Hex2Bytes(reportMetadata.ReportID)
	if len(reportID) != 2 {
		return contracts.TransmissionID{}, fmt.Errorf("%s report ID is of wrong length: %d bytes, expected 2 bytes", userError, len(reportID))
	}

	transmissionID := contracts.TransmissionID{
		Receiver:            common.BytesToAddress(request.Receiver),
		WorkflowExecutionID: [32]byte(rawExecutionID),
		ReportID:            [2]byte(reportID),
	}
	return transmissionID, nil
}

func (e *WriteReport) fetchTransactionReceiptAndCreateReply(ctx context.Context, txHash evmtypes.Hash, receiverStatus evm.ReceiverContractExecutionStatus, errorMessage *string) (*evm.WriteReportReply, error) {
	txReceipt, err := withQuickRetry(ctx, e.lggr, func(ctx context.Context) (*evmtypes.Receipt, error) {
		return e.GetTransactionReceipt(ctx, evmtypes.GeTransactionReceiptRequest{
			Hash:       txHash,
			IsExternal: false, // since we do not run consensus on the receipt itself, it's fine to skip additional versions for external receipts.
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction receipt: %w", err)
	}
	transactionFee, err := withQuickRetry(ctx, e.lggr, func(ctx context.Context) (*evmtypes.TransactionFee, error) {
		return e.CalculateTransactionFee(ctx, evmtypes.ReceiptGasInfo{
			GasUsed:           txReceipt.GasUsed,
			EffectiveGasPrice: txReceipt.EffectiveGasPrice,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("failed to calculate transaction fee: %w", err)
	}
	message := errorMessage
	if receiverStatus == evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED && errorMessage == nil {
		message = ptr("Receiver contract execution failure")
	}

	txStatus := evm.TxStatus_TX_STATUS_SUCCESS
	if txReceipt.Status == 0 {
		txStatus = evm.TxStatus_TX_STATUS_REVERTED
	}

	e.lggr.Infow("Successfully fetched tx receipt",
		"txHash", hex.EncodeToString(txHash[:]),
		"txStatus", txReceipt.Status,
		"transactionFeeWei", transactionFee.TransactionFee.String(),
		"receiverStatus", receiverStatus,
		"errorMessage", message)

	return &evm.WriteReportReply{
		TxHash:                          (txHash)[:],
		TxStatus:                        txStatus,
		TransactionFee:                  pb.NewBigIntFromInt(transactionFee.TransactionFee),
		ReceiverContractExecutionStatus: &receiverStatus,
		ErrorMessage:                    message,
	}, nil
}

func ptr(s string) *string {
	return &s
}

func (e *EVM) validateInputsAndReportMetadata(requestMetadata capabilities.RequestMetadata, request *evm.WriteReportRequest) error {
	if request == nil {
		return errors.New("nil WriteReportRequest")
	}
	if request.Report == nil {
		return errors.New("nil SignedReport in WriteReportRequest")
	}
	if len(request.Receiver) != common.AddressLength {
		return fmt.Errorf("received address is not 20 bytes long. Address in HEX: %s", hex.EncodeToString(request.Receiver))
	}
	if len(request.Report.Sigs) == 0 {
		return fmt.Errorf("no signatures provided")
	}

	reportMetadata, err := decodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return err
	}

	if reportMetadata.Version != 1 {
		return fmt.Errorf("unsupported report version: %d", reportMetadata.Version)
	}

	if reportMetadata.ExecutionID != requestMetadata.WorkflowExecutionID {
		return fmt.Errorf("workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata. Report WorkflowExecutionID: %s, request WorkflowExecutionID: %s", reportMetadata.ExecutionID, requestMetadata.WorkflowExecutionID)
	}

	// case-insensitive verification of the owner address (so that a check-summed address matches its non-checksummed version).
	if !strings.EqualFold(reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner) {
		return fmt.Errorf("workflowOwner in the report does not match WorkflowOwner in the request metadata. Report WorkflowOwner: %s, request WorkflowOwner: %s", reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner)
	}

	//	workflowNames are padded to 10bytes
	decodedName := []byte(requestMetadata.WorkflowName)
	var workflowName [20]byte
	copy(workflowName[:], decodedName)
	if !bytes.Equal([]byte(reportMetadata.WorkflowName[:]), workflowName[:]) {
		return fmt.Errorf("workflowName in the report does not match WorkflowName in the request metadata. Report WorkflowName: %s, request WorkflowName: %s", reportMetadata.WorkflowName, hex.EncodeToString(workflowName[:]))
	}

	if reportMetadata.WorkflowID != requestMetadata.WorkflowID {
		return fmt.Errorf("workflowID in the report does not match WorkflowID in the request metadata. Report WorkflowID: %s, request WorkflowID: %s", reportMetadata.WorkflowID, requestMetadata.WorkflowID)
	}

	return nil
}

// Helper to retrieve TX Hash based on log event executed after processing a report.
type TxHashRetriever struct {
	transmissionID          contracts.TransmissionID
	keystoneForwarderClient contracts.CREForwarderClient
	lggr                    logger.Logger
}

func NewTxHashRetriever(forwarderClient contracts.CREForwarderClient, lggr logger.Logger, transmissionID contracts.TransmissionID) TxHashRetriever {
	return TxHashRetriever{lggr: lggr, keystoneForwarderClient: forwarderClient, transmissionID: transmissionID}
}

// parseReportResult extracts the boolean result from the log data.
// The ReportProcessed event has a non-indexed `result` bool parameter.
// Returns an error if the log data is malformed.
func parseReportResult(logData []byte) (bool, error) {
	if len(logData) < 32 {
		return false, fmt.Errorf("malformed log data: expected at least 32 bytes, got %d", len(logData))
	}
	return logData[31] == 0x01, nil
}

// logDetails holds parsed information about a ReportProcessed log
type logDetails struct {
	TxHash      evmtypes.Hash
	BlockNumber *big.Int
	IsSuccess   bool
}

func (d logDetails) String() string {
	resultStr := "success"
	if !d.IsSuccess {
		resultStr = "failed"
	}
	blockStr := "<nil>"
	if d.BlockNumber != nil {
		blockStr = d.BlockNumber.String()
	}
	return fmt.Sprintf("hash=%s block=%s result=%s",
		hex.EncodeToString(d.TxHash[:]), blockStr, resultStr)
}

// logDetailsList is a slice of logDetails with a custom String method for logging
type logDetailsList []logDetails

func (l logDetailsList) String() string {
	if len(l) == 0 {
		return "[]"
	}
	parts := make([]string, len(l))
	for i, d := range l {
		parts[i] = d.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// buildLogDetails parses logs and returns detailed information about each.
// Returns an error immediately if any log has malformed data.
func buildLogDetails(logs []*evmtypes.Log) (logDetailsList, error) {
	details := make(logDetailsList, len(logs))
	for i, log := range logs {
		if log == nil {
			return nil, fmt.Errorf("nil log at index %d", i)
		}
		if log.BlockNumber == nil {
			return nil, fmt.Errorf("nil BlockNumber for tx %s", hex.EncodeToString(log.TxHash[:]))
		}
		result, err := parseReportResult(log.Data)
		if err != nil {
			return nil, fmt.Errorf("failed to parse report result for tx %s: %w", hex.EncodeToString(log.TxHash[:]), err)
		}
		details[i] = logDetails{
			TxHash:      log.TxHash,
			BlockNumber: log.BlockNumber,
			IsSuccess:   result,
		}
	}
	return details, nil
}

const failedToRetrieveTxHashErrorMessage = "failed to retrieve tx hash for report"

// fetchAndParseLogs retrieves ReportProcessed logs with retry logic and parses them into logDetails.
// Returns an error if no logs are found or if any log data is malformed.
func (thr *TxHashRetriever) fetchAndParseLogs(ctx context.Context) (logDetailsList, error) {
	logs, err := withPollingRetry(ctx, thr.lggr, func(ctx context.Context) ([]*evmtypes.Log, error) {
		retrievedLogs, retrieveErr := thr.keystoneForwarderClient.GetReportProcessedEvents(ctx, thr.transmissionID.Receiver, thr.transmissionID.WorkflowExecutionID, thr.transmissionID.ReportID)
		if retrieveErr != nil {
			return nil, retrieveErr
		}
		if len(retrievedLogs) == 0 {
			return nil, errors.New("no logs found yet, retrying")
		}
		return retrievedLogs, nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", failedToRetrieveTxHashErrorMessage, err)
	}

	if len(logs) == 0 {
		return nil, fmt.Errorf("no logs found for transmission: %s", thr.transmissionID.GetDebugID())
	}

	details, err := buildLogDetails(logs)
	if err != nil {
		return nil, fmt.Errorf("malformed log data for transmission %s: %w", thr.transmissionID.GetDebugID(), err)
	}

	return details, nil
}

// GetSuccessfulTransmissionHash finds and returns the hash of a successful transmission.
// If multiple logs exist, it searches for one with IsSuccess=true.
// Returns an error if no successful transmission is found or if any log data is malformed.
func (thr *TxHashRetriever) GetSuccessfulTransmissionHash(ctx context.Context) (*evmtypes.Hash, error) {
	details, err := thr.fetchAndParseLogs(ctx)
	if err != nil {
		return nil, err
	}

	for _, d := range details {
		if d.IsSuccess {
			return &d.TxHash, nil
		}
	}

	thr.lggr.Errorw("No successful transmission found", append(thr.transmissionID.GetIDPartsForDebugging(), "txCount", len(details), "transactions", details.String())...)
	return nil, fmt.Errorf("no successful transmission found for: %s. Found %d transactions (all failed): %s",
		thr.transmissionID.GetDebugID(), len(details), details)
}

// GetFailedTransmissionHash finds and returns the hash of the earliest failed transmission.
// Returns the oldest log (by block number) for consensus consistency across nodes.
// Returns an error if any log has IsSuccess=true (unexpected success) or if any log data is malformed.
func (thr *TxHashRetriever) GetFailedTransmissionHash(ctx context.Context) (*evmtypes.Hash, error) {
	details, err := thr.fetchAndParseLogs(ctx)
	if err != nil {
		return nil, err
	}

	for _, d := range details {
		if d.IsSuccess {
			return nil, fmt.Errorf("%w for: %s, successful tx hash: %s",
				ErrUnexpectedSuccessfulTransmission, thr.transmissionID.GetDebugID(), hex.EncodeToString(d.TxHash[:]))
		}
	}

	earliestIdx := 0
	for i, d := range details {
		if d.BlockNumber.Cmp(details[earliestIdx].BlockNumber) < 0 {
			earliestIdx = i
		}
	}

	thr.lggr.Debugw("Returning earliest failed transmission",
		append(thr.transmissionID.GetIDPartsForDebugging(),
			"txCount", len(details),
			"selectedTxHash", hex.EncodeToString(details[earliestIdx].TxHash[:]),
			"selectedBlock", details[earliestIdx].BlockNumber.String(),
			"transactions", details.String())...)

	return &details[earliestIdx].TxHash, nil
}

func (e *EVM) isUserErrorWriteReport(err error) bool {
	return strings.HasPrefix(err.Error(), userError)
}
