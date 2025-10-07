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

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
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

func decodeReportMetadata(data []byte) (ocrtypes.Metadata, error) {
	metadata, _, err := ocrtypes.Decode(data)
	return metadata, err
}

func (e *EVM) WriteReport(ctx context.Context, metadata capabilities.RequestMetadata, input *evm.WriteReportRequest) (*capabilities.ResponseAndMetadata[*evm.WriteReportReply], error) {
	ctx = metadata.ContextWithCRE(ctx)
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportInitiated(telemetryContext, input))
	err := validateInputsAndReportMetadata(metadata, input)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport due to invalid request", err.Error()))
		return nil, capabilities.NewRemoteReportableError(err)
	}

	report, billingMetadata, err := e.executeWriteReport(ctx, input, metadata, telemetryContext)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport while checking if the report exists or trying to publish on chain", err.Error()))
		return nil, capabilities.NewRemoteReportableError(err)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully WriteReport execution", e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportSuccess(telemetryContext, input))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.WriteReportReply]{
		Response:         report,
		ResponseMetadata: billingMetadata,
	}
	return &responseAndMetadata, nil
}

func (e *EVM) getFee(ctx context.Context, txIdempotencyKey string) (*big.Float, error) {
	if txIdempotencyKey == "" {
		return nil, fmt.Errorf("txIdempotencyKey is empty, cannot retrieve transaction fee")
	}

	feeInWei, errTxFee := e.EVMService.GetTransactionFee(ctx, txIdempotencyKey)
	if errTxFee != nil {
		return nil, fmt.Errorf("failed to get transaction fee: %w", errTxFee)
	}
	feeInEth := new(big.Float).Quo(new(big.Float).SetInt(feeInWei.TransactionFee), big.NewFloat(1e18))
	e.lggr.Debugw("WriteReport fee", "feeInEth", feeInEth.String(), "feeInWei", feeInWei.TransactionFee.String())
	return feeInEth, nil
}

func (e *EVM) executeWriteReport(ctx context.Context, request *evm.WriteReportRequest, metadata capabilities.RequestMetadata, telemetryContext monitoring.TelemetryContext) (*evm.WriteReportReply, capabilities.ResponseMetadata, error) {
	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	if request.GasConfig == nil {
		request.GasConfig = &evm.GasConfig{}
		request.GasConfig.GasLimit, err = e.txGasLimit.Limit(ctx)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, err
		}
	} else {
		err = e.txGasLimit.Check(ctx, request.GasConfig.GasLimit)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, err
		}
	}

	var transmissionInfo contracts.TransmissionInfo
	transmissionInfo, err = e.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	txHashRetriever := NewTxHashRetriever(e.forwarderClient, e.lggr, transmissionID)

	switch transmissionInfo.State {
	case TransmissionStateNotAttempted:
		e.lggr.Infow("transmission not attempted - attempting to push to txmgr", "request", request, "reportLen", len(request.Report.RawReport), "reportContextLen", len(request.Report.ReportContext), "nSignatures", len(request.Report.Sigs), "executionID", metadata.WorkflowExecutionID)
	case TransmissionStateSucceeded:
		e.lggr.Infow("returning without a transmission attempt - report already onchain ", "executionID", metadata.WorkflowExecutionID)
		txHash, err := txHashRetriever.GetHash(ctx)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, err
		}
		reply, err := e.fetchTransactionReceiptAndCreateReply(ctx, *txHash, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS, nil)
		return reply, capabilities.ResponseMetadata{}, err
	case TransmissionStateInvalidReceiver:
		e.lggr.Infow("transmission already done by another node but failed due to invalid receiver, not reattempting")
		txHash, err := txHashRetriever.GetHash(ctx)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, err
		}
		reply, err := e.processUnrecoverableTxState(ctx, request, metadata, *txHash, transmissionInfo, transmissionID, true)
		return reply, capabilities.ResponseMetadata{}, err
	case TransmissionStateFailed:
		receiverGasMinimum := e.ReceiverGasMinimum
		if request.GasConfig != nil && request.GasConfig.GasLimit > 0 {
			receiverGasMinimum = request.GasConfig.GasLimit - contracts.ForwarderContractLogicGasCost
		}
		if transmissionInfo.GasLimit.Uint64() > receiverGasMinimum {
			e.lggr.Infow("returning without a transmission attempt - transmission already attempted and failed, sufficient gas was provided", "executionID", metadata.WorkflowExecutionID, "receiverGasMinimum", receiverGasMinimum, "transmissionGasLimit", transmissionInfo.GasLimit)
			txHash, err := txHashRetriever.GetHash(ctx)
			if err != nil {
				return nil, capabilities.ResponseMetadata{}, err
			}
			reply, err := e.fetchTransactionReceiptAndCreateReply(ctx, *txHash, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, nil)
			return reply, capabilities.ResponseMetadata{}, err
		}
		e.lggr.Infow("retrying a failed transmission - attempting to push to txmgr", "request", request, "reportLen", len(request.Report.RawReport), "reportContextLen", len(request.Report.ReportContext), "nSignatures", len(request.Report.Sigs), "executionID", metadata.WorkflowExecutionID, "receiverGasMinimum", receiverGasMinimum, "transmissionGasLimit", transmissionInfo.GasLimit)
	default:
		return fatalWriteReportReply(getInvalidStateErrorMessage(transmissionInfo.State)), capabilities.ResponseMetadata{}, nil
	}

	err = e.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	e.lggr.Debugw("Submitting transaction for report", "request", request)
	transactionResult, err := e.forwarderClient.InvokeOnReport(ctx, transmissionID.Receiver, request.Report, request.GasConfig)
	if err != nil {
		e.lggr.Error("Transaction failed", "request", request)
		return &evm.WriteReportReply{
			TxStatus:     evm.TxStatus_TX_STATUS_FATAL,
			ErrorMessage: ptr(err.Error()),
		}, capabilities.ResponseMetadata{}, nil
	}

	strategy := retry.Strategy[contracts.TransmissionInfo]{
		Backoff: &backoff.Backoff{
			Factor: 2,
			Max:    2 * time.Second,
			Min:    100 * time.Millisecond,
		},
		MaxRetries: 5,
	}
	retryContext, cancelFunc := context.WithTimeout(ctx, 50*time.Second)
	defer cancelFunc()

	transmissionInfo, err = strategy.Do(retryContext, e.lggr, func(ctx context.Context) (contracts.TransmissionInfo, error) {
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

	txHashRetriever.Reset()

	var meteringMetadata capabilities.ResponseMetadata
	transactionFee, err := e.getFee(ctx, transactionResult.TxIdempotencyKey)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildWriteReportTxFeeCalculationError(telemetryContext, request, transactionResult.TxIdempotencyKey, err.Error()))
	} else {
		meteringMetadata = metering.GetResponseMetadataWriteReport(transactionFee, e.chainSelector)
	}

	// This is counterintuitive, but the tx manager is currently returning unconfirmed whenever the tx is confirmed
	// current implementation here: https://github.com/smartcontractkit/chainlink-framework/blob/main/chains/txmgr/txmgr.go#L697
	// so we need to check if we were able to write to the consumer contract to determine if the transaction was successful
	switch transmissionInfo.State {
	case TransmissionStateSucceeded:
		e.lggr.Debugw("Transaction confirmed", "request", request)
		reply, err := e.fetchTransactionReceiptAndCreateReply(ctx, transactionResult.TxHash, evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS, nil)
		return reply, meteringMetadata, err
	case TransmissionStateFailed, TransmissionStateInvalidReceiver:
		reply, err := e.processUnrecoverableTxState(ctx, request, metadata, transactionResult.TxHash, transmissionInfo, transmissionID, true)
		return reply, meteringMetadata, err
	default:
		return nil, meteringMetadata, fmt.Errorf("transmission state not expected at this point, tx state is: %d", transmissionInfo.State)
	}
}

func getInvalidStateErrorMessage(state uint8) string {
	return fmt.Sprintf("unexpected transmission state: %v", state)
}

func (e *EVM) processUnrecoverableTxState(ctx context.Context, request *evm.WriteReportRequest, metadata capabilities.RequestMetadata, txHash evmtypes.Hash, transmissionInfo contracts.TransmissionInfo, transmissionID contracts.TransmissionID, txAttemptedLocally bool) (*evm.WriteReportReply, error) {
	if !txAttemptedLocally {
		e.lggr.Infow("returning without a transmission attempt - transmission already attempted, receiver was marked as invalid", "executionID", metadata.WorkflowExecutionID)
	} else {
		e.lggr.Errorw("Transaction written to the forwarder, but failed to be written to the consumer contract", "request", request, "transmissionState", transmissionInfo.State)
	}
	var message *string
	if transmissionInfo.State == TransmissionStateInvalidReceiver {
		message = getInvalidReceiverMessage(transmissionID.Receiver[:])
	} else {
		message = ptr(UnknownIssueExecutingReceiverContractMessage)
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

	reportMetadata, err := decodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return contracts.TransmissionID{}, err
	}

	reportID := common.Hex2Bytes(reportMetadata.ReportID)
	if len(reportID) != 2 {
		return contracts.TransmissionID{}, fmt.Errorf("report ID is of wrong length: %d bytes, expected 2 bytes", len(reportMetadata.ReportID))
	}

	transmissionID := contracts.TransmissionID{
		Receiver:            common.BytesToAddress(request.Receiver),
		WorkflowExecutionID: [32]byte(rawExecutionID),
		ReportID:            [2]byte(reportID),
	}
	return transmissionID, nil
}

func (e *EVM) fetchTransactionReceiptAndCreateReply(ctx context.Context, txHash evmtypes.Hash, receiverStatus evm.ReceiverContractExecutionStatus, errorMessage *string) (*evm.WriteReportReply, error) {
	// TODO: PLEX-1524 - we need retry logic here in case the underlying RPC is lagging behind the one that submitted the TX.
	txReceipt, err := e.EVMService.GetTransactionReceipt(ctx, evmtypes.GeTransactionReceiptRequest{
		Hash:       txHash,
		IsExternal: false, // since we do not run consensus on the receipt itself, it's fine to skip additional versions for external receipts.
	})
	if err != nil {
		return nil, err
	}
	transactionFee, err := e.EVMService.CalculateTransactionFee(ctx, evmtypes.ReceiptGasInfo{
		GasUsed:           txReceipt.GasUsed,
		EffectiveGasPrice: txReceipt.EffectiveGasPrice,
	})
	if err != nil {
		return nil, err
	}
	message := errorMessage
	if receiverStatus.Enum() == evm.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED.Enum() && errorMessage == nil {
		message = ptr("Receiver contract execution failure")
	}
	return &evm.WriteReportReply{
		TxHash:                          (txHash)[:],
		TxStatus:                        evm.TxStatus_TX_STATUS_SUCCESS,
		TransactionFee:                  pb.NewBigIntFromInt(transactionFee.TransactionFee),
		ReceiverContractExecutionStatus: &receiverStatus,
		ErrorMessage:                    message,
	}, nil
}

func ptr(s string) *string {
	return &s
}

func fatalWriteReportReply(message string) *evm.WriteReportReply {
	return &evm.WriteReportReply{
		ErrorMessage: &message,
		TxStatus:     evm.TxStatus_TX_STATUS_FATAL,
	}
}

func validateInputsAndReportMetadata(requestMetadata capabilities.RequestMetadata, request *evm.WriteReportRequest) error {
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
	txHash                  *evmtypes.Hash
}

func NewTxHashRetriever(forwarderClient contracts.CREForwarderClient, lggr logger.Logger, transmissionID contracts.TransmissionID) TxHashRetriever {
	return TxHashRetriever{lggr: lggr, keystoneForwarderClient: forwarderClient, transmissionID: transmissionID}
}

func (thr *TxHashRetriever) Reset() {
	thr.txHash = nil
}

const failedToRetrieveTxHashErrorMessage = "Failed to retrieve TX HASH for report"

func (thr *TxHashRetriever) GetHash(ctx context.Context) (*evmtypes.Hash, error) {
	if thr.txHash != nil {
		return thr.txHash, nil
	}

	logs, err := thr.keystoneForwarderClient.GetReportProcessedEvents(ctx, thr.transmissionID.Receiver, thr.transmissionID.WorkflowExecutionID, thr.transmissionID.ReportID)
	if err != nil {
		thr.lggr.Debugw(failedToRetrieveTxHashErrorMessage, thr.transmissionID.GetIDPartsForDebugging()...)
		return nil, errors.Join(err, fmt.Errorf("%s: %w", failedToRetrieveTxHashErrorMessage, err))
	}
	if len(logs) > 1 {
		thr.lggr.Debugw("found more than one log associated to report transmission", thr.transmissionID.GetIDPartsForDebugging()...)
		return nil, fmt.Errorf("We found more than one TX Hash for: %s", thr.transmissionID.GetDebugID())
	}
	if len(logs) == 0 {
		thr.lggr.Debugw("no log associated to report transmission found", thr.transmissionID.GetIDPartsForDebugging()...)
		return nil, fmt.Errorf("no log found but a log was executed for transmission ID: %+v", thr.transmissionID)
	}
	thr.txHash = &logs[0].TxHash
	return thr.txHash, nil
}
