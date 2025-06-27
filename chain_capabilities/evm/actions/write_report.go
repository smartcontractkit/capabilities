package actions

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts"
)

const (
	TransmissionStateNotAttempted uint8 = iota
	TransmissionStateSucceeded
	TransmissionStateInvalidReceiver
	TransmissionStateFailed
)

const UnknownIssueExecutingReceiverContractMessage = "unknown issue execution receiver contract"

// TODO from chainlink/core/platform - we should have this in common
// Observability keys
const (
	KeyCapabilityID        = "capabilityID"
	KeyTriggerID           = "triggerID"
	KeyWorkflowID          = "workflowID"
	KeyWorkflowExecutionID = "workflowExecutionID"
	KeyWorkflowName        = "workflowName"
	KeyWorkflowVersion     = "workflowVersion"
	KeyWorkflowOwner       = "workflowOwner"
	KeyStepID              = "stepID"
	KeyStepRef             = "stepRef"
	KeyDonID               = "DonID"
	KeyDonF                = "F"
	KeyDonN                = "N"
	KeyDonQ                = "Q"
	KeyP2PID               = "p2pID"
	ValueWorkflowVersion   = "1.0.0"
	ValueWorkflowVersionV2 = "2.0.0"
)

func decodeReportMetadata(data []byte) (ocrtypes.Metadata, error) {
	metadata, _, err := ocrtypes.Decode(data)
	return metadata, err
}

func (e EVM) WriteReport(ctx context.Context, metadata capabilities.RequestMetadata, input *evmcap.WriteReportRequest) (*evmcap.WriteReportReply, error) {
	err := validateInputsAndReportMetadata(metadata, input)
	if err != nil {
		return nil, err
	}
	return e.executeWriteReport(ctx, metadata, input)
}

func (e EVM) executeWriteReport(ctx context.Context, metadata capabilities.RequestMetadata, request *evmcap.WriteReportRequest) (*evmcap.WriteReportReply, error) {
	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return nil, err
	}

	// Check whether value was already transmitted on chain
	transmissionInfo, err := e.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
	if err != nil {
		return nil, err
	}

	txHashRetriever := NewTxHashRetriever(e.forwarderClient, e.lggr, transmissionID)

	switch transmissionInfo.State {
	case TransmissionStateNotAttempted:
		e.lggr.Infow("transmission not attempted - attempting to push to txmgr", "request", request, "reportLen", len(request.Report.RawReport), "reportContextLen", len(request.Report.ReportContext), "nSignatures", len(request.Report.Signatures), "executionID", metadata.WorkflowExecutionID)
	case TransmissionStateSucceeded:
		e.lggr.Infow("returning without a transmission attempt - report already onchain ", "executionID", metadata.WorkflowExecutionID)
		txHash, err := txHashRetriever.GetHash(ctx)
		if err != nil {
			return nil, err
		}
		return e.fetchTransactionReceiptAndCreateReply(ctx, *txHash, evmcap.ReceiverContractExecutionStatus_SUCCESS, nil)
	case TransmissionStateInvalidReceiver:
		txHash, err := txHashRetriever.GetHash(ctx)
		if err != nil {
			return nil, err
		}
		return e.processUnrecoverableTxState(ctx, request, metadata, *txHash, transmissionInfo, transmissionID, true)
	case TransmissionStateFailed:
		receiverGasMinimum := e.ReceiverGasMinimum
		if request.GasConfig != nil && request.GasConfig.GasLimit > 0 {
			receiverGasMinimum = request.GasConfig.GasLimit - contracts.ForwarderContractLogicGasCost
		}
		if transmissionInfo.GasLimit.Uint64() > receiverGasMinimum {
			e.lggr.Infow("returning without a transmission attempt - transmission already attempted and failed, sufficient gas was provided", "executionID", metadata.WorkflowExecutionID, "receiverGasMinimum", receiverGasMinimum, "transmissionGasLimit", transmissionInfo.GasLimit)
			txHash, err := txHashRetriever.GetHash(ctx)
			if err != nil {
				return nil, err
			}
			return e.fetchTransactionReceiptAndCreateReply(ctx, *txHash, evmcap.ReceiverContractExecutionStatus_REVERTED, nil)
		}
		e.lggr.Infow("retrying a failed transmission - attempting to push to txmgr", "request", request, "reportLen", len(request.Report.RawReport), "reportContextLen", len(request.Report.ReportContext), "nSignatures", len(request.Report.Signatures), "executionID", metadata.WorkflowExecutionID, "receiverGasMinimum", receiverGasMinimum, "transmissionGasLimit", transmissionInfo.GasLimit)
	default:
		return fatalWriteReportReply(getInvalidStateErrorMessage(transmissionInfo.State)), nil
	}

	e.lggr.Debugw("Submitting transaction for report", "request", request)

	transactionResult, err := e.forwarderClient.InvokeOnReport(ctx, transmissionID.Receiver, request.Report, request.GasConfig)
	if err != nil {
		e.lggr.Error("Transaction failed", "request", request)
		// TODO add beholder ticket.
		// msg := "transaction failed to be written to the forwarder, transmission ID: " + transmissionID.GetDebugID()
		// err = c.emitter.With(
		// 	KeyWorkflowID, metadata.WorkflowID,
		// 	KeyWorkflowName, metadata.DecodedWorkflowName,
		// 	KeyWorkflowOwner, metadata.WorkflowOwner,
		// 	KeyWorkflowExecutionID, metadata.WorkflowExecutionID,
		// ).Emit(ctx, msg)
		// if err != nil {
		// 	c.lggr.Errorf("failed to send custom message with msg: %s, err: %v", msg, err)
		// }
		return &evmcap.WriteReportReply{
			TxStatus:     evm.TxStatus_TX_FATAL,
			ErrorMessage: ptr(err.Error()),
		}, nil
	}

	// PLEX-1524 - improve this since it may be using an RPC that's lagging related to the one that submitted the TX.
	transmissionInfo, err = e.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
	if err != nil {
		return nil, err
	}

	txHashRetriever.Reset()
	txHash := transactionResult.TxHash

	// This is counterintuitive, but the tx manager is currently returning unconfirmed whenever the tx is confirmed
	// current implementation here: https://github.com/smartcontractkit/chainlink-framework/blob/main/chains/txmgr/txmgr.go#L697
	// so we need to check if we were able to write to the consumer contract to determine if the transaction was successful
	switch transmissionInfo.State {
	case TransmissionStateSucceeded:
		e.lggr.Debugw("Transaction confirmed", "request", request)
		return e.fetchTransactionReceiptAndCreateReply(ctx, txHash, evmcap.ReceiverContractExecutionStatus_SUCCESS, nil)
	case TransmissionStateFailed, TransmissionStateInvalidReceiver:
		return e.processUnrecoverableTxState(ctx, request, metadata, txHash, transmissionInfo, transmissionID, true)
	default:
		return nil, fmt.Errorf("Transmission state not expected at this point, tx state is: %d", transmissionInfo.State)
	}
}

func getInvalidStateErrorMessage(state uint8) string {
	return fmt.Sprintf("unexpected transmission state: %v", state)
}

func (e EVM) processUnrecoverableTxState(ctx context.Context, request *evmcap.WriteReportRequest, metadata capabilities.RequestMetadata, txHash evmtypes.Hash, transmissionInfo contracts.TransmissionInfo, transmissionID contracts.TransmissionID, txAttemptedLocally bool) (*evmcap.WriteReportReply, error) {
	if !txAttemptedLocally {
		e.lggr.Infow("returning without a transmission attempt - transmission already attempted, receiver was marked as invalid", "executionID", metadata.WorkflowExecutionID)
	} else {
		e.lggr.Errorw("Transaction written to the forwarder, but failed to be written to the consumer contract", "request", request, "transmissionState", transmissionInfo.State)
		// TODO Add link to configure emitter in the capability.
		// msg := "transaction written to the forwarder, but failed to be written to the consumer contract, transaction hash: " + common.Bytes2Hex((*txHash)[:])
		// err = c.emitter.With(
		// 	KeyWorkflowID, metadata.WorkflowID,
		// 	KeyWorkflowName, metadata.DecodedWorkflowName,
		// 	KeyWorkflowOwner, metadata.WorkflowOwner,
		// 	KeyWorkflowExecutionID, metadata.WorkflowExecutionID,
		// ).Emit(ctx, msg)
		// if err != nil {
		// 	c.lggr.Errorf("failed to send custom message with msg: %s, err: %v", msg, err)
		// }
	}
	var message *string
	if transmissionInfo.State == TransmissionStateInvalidReceiver {
		message = getInvalidReceiverMessage(transmissionID.Receiver[:])
	} else {
		message = ptr(UnknownIssueExecutingReceiverContractMessage)
	}
	return e.fetchTransactionReceiptAndCreateReply(ctx, txHash, evmcap.ReceiverContractExecutionStatus_REVERTED, message)
}

func getInvalidReceiverMessage(receiver []byte) *string {
	return ptr(fmt.Sprintf("Invalid receiver: %s", common.Bytes2Hex(receiver)))
}

func getTransmissionID(workflowExecutionID string, request *evmcap.WriteReportRequest) (contracts.TransmissionID, error) {
	rawExecutionID, err := hex.DecodeString(workflowExecutionID)
	if err != nil {
		return contracts.TransmissionID{}, err
	}

	transmissionID := contracts.TransmissionID{
		Receiver:            common.BytesToAddress(request.Receiver),
		WorkflowExecutionID: [32]byte(rawExecutionID),
		ReportID:            [2]byte(request.Report.Id),
	}
	return transmissionID, nil
}

func (e EVM) fetchTransactionReceiptAndCreateReply(ctx context.Context, txHash evmtypes.Hash, receiverStatus evmcap.ReceiverContractExecutionStatus, errorMessage *string) (*evmcap.WriteReportReply, error) {
	// TODO: PLEX-1524 - we need retry logic here in case the underlying RPC is lagging behind the one that submitted the TX.
	txReceipt, err := e.EVMService.GetTransactionReceipt(ctx, txHash)
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
	if receiverStatus.Enum() == evmcap.ReceiverContractExecutionStatus_REVERTED.Enum() && errorMessage == nil {
		message = ptr("Receiver contract execution failure")
	}
	return &evmcap.WriteReportReply{
		TxHash:                          (txHash)[:],
		TxStatus:                        evm.TxStatus_TX_SUCCESS,
		TransactionFee:                  pb.NewBigIntFromInt(transactionFee.TransactionFee),
		ReceiverContractExecutionStatus: &receiverStatus,
		ErrorMessage:                    message,
	}, nil
}

// TODO remove this should already exists in some common
func ptr(s string) *string {
	return &s
}

func fatalWriteReportReply(message string) *evmcap.WriteReportReply {
	return &evmcap.WriteReportReply{
		ErrorMessage: &message,
		TxStatus:     evm.TxStatus_TX_FATAL,
	}
}

func validateInputsAndReportMetadata(requestMetadata capabilities.RequestMetadata, request *evmcap.WriteReportRequest) error {
	if request == nil {
		return errors.New("nil WriteReportRequest")
	}
	if request.Report == nil {
		return errors.New("nil SignedReport in WriteReportRequest")
	}
	if len(request.Receiver) != common.AddressLength {
		return fmt.Errorf("received address is not 20 bytes long. Address in HEX: %s", hex.EncodeToString(request.Receiver))
	}
	if len(request.Report.Signatures) == 0 {
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
		return fmt.Errorf("workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata. Report WorkflowExecutionID: %+v, request WorkflowExecutionID: %+v", reportMetadata.ExecutionID, requestMetadata.WorkflowExecutionID)
	}

	// case-insensitive verification of the owner address (so that a check-summed address matches its non-checksummed version).
	if !strings.EqualFold(reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner) {
		return fmt.Errorf("workflowOwner in the report does not match WorkflowOwner in the request metadata. Report WorkflowOwner: %+v, request WorkflowOwner: %+v", reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner)
	}

	// workflowNames are padded to 10bytes
	decodedName := []byte(requestMetadata.WorkflowName)
	if err != nil {
		return err
	}
	var workflowName [20]byte
	copy(workflowName[:], decodedName)
	if !bytes.Equal([]byte(reportMetadata.WorkflowName[:]), workflowName[:]) {
		return fmt.Errorf("workflowName in the report does not match WorkflowName in the request metadata. Report WorkflowName: %+v, request WorkflowName: %+v", reportMetadata.WorkflowName, hex.EncodeToString(workflowName[:]))
	}

	if reportMetadata.WorkflowID != requestMetadata.WorkflowID {
		return fmt.Errorf("workflowID in the report does not match WorkflowID in the request metadata. Report WorkflowID: %+v, request WorkflowID: %+v", reportMetadata.WorkflowID, requestMetadata.WorkflowID)
	}

	if !bytes.Equal([]byte(reportMetadata.ReportID), request.Report.Id) {
		return fmt.Errorf("reportID in the report does not match ReportID in the inputs. reportMetadata.ReportID: %x, Inputs.SignedReport.ID: %x", reportMetadata.ReportID, request.Report.Id)
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
		return nil, nil
	}
	thr.txHash = &logs[0].TxHash
	return thr.txHash, nil
}
