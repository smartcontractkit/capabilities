package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	types "github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/metering"
)

const ocrSignatureLen = 65

type writeReport struct {
	service                  types.StellarService
	forwarderClient          CREForwarderClient
	lggr                     logger.SugaredLogger
	forwarderLookbackLedgers int64
	chainSelector            uint64
	reportSizeLimit          limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler    ts.TransmissionScheduler
}

func (s *Stellar) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *stellarcap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		return nil, NewUserError(err, caperrors.InvalidArgument)
	}

	wr := &writeReport{
		service:                  s.StellarService,
		forwarderClient:          s.forwarderClient,
		lggr:                     s.lggr,
		forwarderLookbackLedgers: s.forwarderLookbackLedgers,
		chainSelector:            s.chainSelector,
		reportSizeLimit:          s.reportSizeLimit,
		transmissionScheduler:    s.transmissionScheduler,
	}

	reply, meteringMeta, err := wr.execute(ctx, metadata, input)
	if err != nil {
		return nil, GetError(err, s.isUserErrorWriteReport(err))
	}

	return &capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply]{
		Response:         reply,
		ResponseMetadata: meteringMeta,
	}, nil
}

func (wr *writeReport) execute(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	request *stellarcap.WriteReportRequest,
) (*stellarcap.WriteReportReply, capabilities.ResponseMetadata, error) {
	ctx = contexts.WithChainSelector(ctx, wr.chainSelector)

	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	scheduleKey, err := transmissionID.ScheduleKey()
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	queuePosition := wr.transmissionScheduler.GetQueuePosition(hex.EncodeToString(scheduleKey[:]))
	wr.lggr = wr.lggr.With(append([]any{"queuePosition", queuePosition, "forwarder", wr.forwarderClient.ForwarderAddress()}, transmissionID.LogAttrs()...)...)

	txHashRetriever := NewTxHashRetriever(wr.forwarderClient, wr.lggr, transmissionID)

	info, err := wr.pollTransmissionInfo(ctx, transmissionID, queuePosition)
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get transmission info: %w", err)
	}

	switch info.State {
	case TransmissionStateSucceeded:
		txHash, hashErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx)
		if hashErr != nil {
			wr.lggr.Errorw("Returning without a transmission attempt - prior transmission succeeded, but failed to retrieve its tx hash", "error", hashErr)
			return nil, capabilities.ResponseMetadata{}, hashErr
		}
		reply := wr.successReplyFromObservedState(info)
		if err := wr.populateReplyFromTx(ctx, reply, txHash); err != nil {
			return nil, capabilities.ResponseMetadata{}, err
		}
		return reply, capabilities.ResponseMetadata{}, nil
	case TransmissionStateInvalidReceiver:
		txHash, hashErr := txHashRetriever.GetFailedTransmissionHash(ctx)
		if hashErr != nil {
			wr.lggr.Errorw("Returning without a transmission attempt - prior transmission marked receiver invalid, but failed to retrieve its tx hash", "error", hashErr)
			return nil, capabilities.ResponseMetadata{}, hashErr
		}
		reply := wr.revertedReplyFromObservedState(info, "receiver contract cannot accept reports: not a Wasm contract or missing on_report function")
		if err := wr.populateReplyFromTx(ctx, reply, txHash); err != nil {
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s receiver contract cannot accept reports: not a Wasm contract or missing on_report function: additional error while fetching tx details: %w", capcommon.UserError, err)
		}
		return reply, capabilities.ResponseMetadata{}, nil
	case TransmissionStateFailed:
		txHash, hashErr := txHashRetriever.GetFailedTransmissionHash(ctx)
		if hashErr != nil {
			if errors.Is(hashErr, ErrUnexpectedSuccessfulTransmission) {
				wr.lggr.Errorw("Returning without a transmission attempt - unexpected successful transmission while state is failed", "error", hashErr)
			} else {
				wr.lggr.Errorw("Returning without a transmission attempt - prior transmission failed, but failed to retrieve its tx hash", "error", hashErr)
			}
			return nil, capabilities.ResponseMetadata{}, hashErr
		}
		reply := wr.revertedReplyFromObservedState(info, "receiver contract execution failed")
		if err := wr.populateReplyFromTx(ctx, reply, txHash); err != nil {
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s receiver contract execution failed: additional error while fetching tx details: %w", capcommon.UserError, err)
		}
		return reply, capabilities.ResponseMetadata{}, nil
	case TransmissionStateNotAttempted:
	default:
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("unexpected transmission state: %d", info.State)
	}

	if err := wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport)); err != nil {
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s report size exceeds limit: %w", capcommon.UserError, err)
	}

	submitResp, err := wr.forwarderClient.InvokeOnReport(ctx, request.ContractId, request.Report)
	if err != nil {
		return nil, capabilities.ResponseMetadata{}, err
	}

	// Poll for the canonical on-chain transmission state. The forwarder may record the
	// outcome after the tx confirms; retry until it is visible or the context expires.
	postInfo, pollErr := capcommon.WithPollingRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readInfo, readErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if readErr != nil {
			return TransmissionInfo{}, readErr
		}
		if readInfo.State == TransmissionStateNotAttempted {
			return TransmissionInfo{}, errors.New("tx submitted but transmission info not yet visible on-chain, retrying")
		}
		return readInfo, nil
	})
	if pollErr != nil {
		// TX was submitted but on-chain state is still not visible; use the TXM result directly.
		wr.lggr.Warnw("Failed to poll transmission info after submit, returning reply from TX outcome", "error", pollErr)
		reply := wr.replyFromOwnTransaction(submitResp)
		return reply, wr.meteringFromReply(reply), nil
	}

	switch postInfo.State {
	case TransmissionStateSucceeded:
		reply := wr.successReplyFromObservedState(postInfo)
		populateReplyFromSubmit(reply, submitResp)
		return reply, wr.meteringFromReply(reply), nil
	case TransmissionStateInvalidReceiver:
		reply := wr.revertedReplyFromObservedState(postInfo, "receiver contract cannot accept reports: not a Wasm contract or missing on_report function")
		populateReplyFromSubmit(reply, submitResp)
		return reply, wr.meteringFromReply(reply), nil
	case TransmissionStateFailed:
		reply := wr.revertedReplyFromObservedState(postInfo, "receiver contract execution failed")
		populateReplyFromSubmit(reply, submitResp)
		return reply, wr.meteringFromReply(reply), nil
	default:
		reply := wr.replyFromOwnTransaction(submitResp)
		return reply, wr.meteringFromReply(reply), nil
	}
}

func (s *Stellar) validateWriteReportInputs(metadata capabilities.RequestMetadata, request *stellarcap.WriteReportRequest) error {
	if request == nil {
		return errors.New("nil WriteReportRequest")
	}
	if request.Report == nil {
		return errors.New("nil SignedReport in WriteReportRequest")
	}
	if request.ContractId == "" {
		return errors.New("contractId is required")
	}
	if _, err := strkey.Decode(strkey.VersionByteContract, request.ContractId); err != nil {
		return fmt.Errorf("%s invalid receiver contract address: %w", capcommon.UserError, err)
	}
	if len(request.Report.Sigs) == 0 {
		return fmt.Errorf("%s signed report must contain at least one signature", capcommon.UserError)
	}
	for i, sig := range request.Report.Sigs {
		if len(sig.GetSignature()) != ocrSignatureLen {
			return fmt.Errorf("%s signature %d has invalid length: got %d, want %d", capcommon.UserError, i, len(sig.GetSignature()), ocrSignatureLen)
		}
	}

	reportMetadata, err := capcommon.DecodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return fmt.Errorf("%s failed to decode report metadata: %w", capcommon.UserError, err)
	}
	if reportMetadata.Version != 1 {
		return fmt.Errorf("%s unsupported report metadata version: %d", capcommon.UserError, reportMetadata.Version)
	}
	if reportMetadata.ExecutionID != metadata.WorkflowExecutionID {
		return fmt.Errorf("%s report workflowExecutionID does not match request metadata", capcommon.UserError)
	}
	if !strings.EqualFold(reportMetadata.WorkflowOwner, metadata.WorkflowOwner) {
		return fmt.Errorf("%s report workflowOwner does not match request metadata", capcommon.UserError)
	}
	expectedWorkflowName := metadata.WorkflowName
	if len(expectedWorkflowName) < 20 {
		expectedWorkflowName += strings.Repeat("0", 20-len(expectedWorkflowName))
	}
	if !strings.EqualFold(reportMetadata.WorkflowName, expectedWorkflowName) {
		return fmt.Errorf("%s report workflowName does not match request metadata", capcommon.UserError)
	}
	if reportMetadata.WorkflowID != metadata.WorkflowID {
		return fmt.Errorf("%s report workflowID does not match request metadata", capcommon.UserError)
	}
	return nil
}

func getTransmissionID(workflowExecutionID string, request *stellarcap.WriteReportRequest) (TransmissionID, error) {
	rawExecutionID, reportID, err := capcommon.ParseTransmissionComponents(workflowExecutionID, request.Report.RawReport)
	if err != nil {
		return TransmissionID{}, err
	}

	return TransmissionID{
		Receiver:            request.ContractId,
		WorkflowExecutionID: rawExecutionID,
		ReportID:            reportID,
	}, nil
}

// pollTransmissionInfo returns the forwarder transmission state at this point in the
// DON schedule. Nodes with queuePosition > 0 wait until queuePosition×DeltaStage before
// submitting, polling with exponential backoff so an earlier peer's success or terminal
// failure can be observed without spending fees on a duplicate submit.
func (wr *writeReport) pollTransmissionInfo(
	ctx context.Context,
	transmissionID TransmissionID,
	queuePosition int,
) (lastValidInfo TransmissionInfo, err error) {
	if queuePosition <= 0 {
		return capcommon.WithQuickRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
			return wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		})
	}

	delay := time.Duration(queuePosition) * wr.transmissionScheduler.DeltaStage
	wr.lggr.Infow("Polling until slot or state change", "delay", delay, "deltaStage", wr.transmissionScheduler.DeltaStage)

	attempt := 0
	stageTimer := time.NewTimer(delay)
	hadSuccessfulPoll := false
	defer stageTimer.Stop()

	for {
		if info, infoErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID); infoErr != nil {
			wr.lggr.Debugw("GetTransmissionInfo failed during polling", "error", infoErr, "attempt", attempt)
		} else {
			hadSuccessfulPoll = true
			lastValidInfo = info
			switch lastValidInfo.State {
			case TransmissionStateSucceeded, TransmissionStateInvalidReceiver, TransmissionStateFailed:
				return lastValidInfo, nil
			case TransmissionStateNotAttempted:
			default:
				wr.lggr.Warnw("Unexpected transmission state during polling, continuing", "state", lastValidInfo.State)
			}
		}

		wait := (100 * time.Millisecond) << min(attempt, 5)
		if wait > 2*time.Second {
			wait = 2 * time.Second
		}
		attempt++

		select {
		case <-ctx.Done():
			return TransmissionInfo{}, fmt.Errorf("timed out waiting for transmission info")
		case <-stageTimer.C:
			if lastValidInfo.State == TransmissionStateNotAttempted {
				if finalInfo, finalErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID); finalErr == nil {
					hadSuccessfulPoll = true
					lastValidInfo = finalInfo
				} else {
					wr.lggr.Debugw("Final GetTransmissionInfo at delta stage boundary failed", "error", finalErr)
				}
			}
			if !hadSuccessfulPoll {
				wr.lggr.Errorw("All GetTransmissionInfo polls failed during delta stage window, cannot determine transmission state")
				return TransmissionInfo{}, fmt.Errorf("all GetTransmissionInfo polls failed during delta stage window")
			}
			wr.lggr.Infow("Delta stage has passed, returning transmission info", "state", lastValidInfo.State, "attempts", attempt)
			return lastValidInfo, nil
		case <-time.After(wait):
		}
	}
}

func (wr *writeReport) meteringFromReply(reply *stellarcap.WriteReportReply) capabilities.ResponseMetadata {
	if reply == nil || reply.TransactionFee == nil {
		return capabilities.ResponseMetadata{}
	}
	return metering.GetResponseMetadataWriteReport(*reply.TransactionFee, wr.chainSelector)
}

func (wr *writeReport) populateReplyFromTx(ctx context.Context, reply *stellarcap.WriteReportReply, txHash string) error {
	txResp, err := capcommon.WithQuickRetry(ctx, wr.lggr, func(ctx context.Context) (stellartypes.GetTransactionResponse, error) {
		return wr.service.GetTransaction(ctx, stellartypes.GetTransactionRequest{TxHash: txHash})
	})
	if err != nil {
		return fmt.Errorf("failed to get transaction for tx hash %s: %w", txHash, err)
	}
	reply.TxHash = capcommon.Ptr(txHash)
	if txResp.FeeStroops > 0 {
		fee := txResp.FeeStroops
		reply.TransactionFee = &fee
	}
	if txResp.LedgerCloseTime > 0 {
		ts := uint64(txResp.LedgerCloseTime) * 1_000_000
		reply.BlockTimestamp = &ts
	}
	if reply.LedgerSequence == nil && txResp.LedgerSequence > 0 {
		reply.LedgerSequence = capcommon.Ptr(txResp.LedgerSequence)
	}
	return nil
}

func (wr *writeReport) successReplyFromObservedState(info TransmissionInfo) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{
		TxStatus: stellarcap.TxStatus_TX_STATUS_SUCCESS,
	}
	status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
	reply.ReceiverContractExecutionStatus = &status
	if info.LedgerSequence != 0 {
		reply.LedgerSequence = capcommon.Ptr(info.LedgerSequence)
	}
	return reply
}

func (wr *writeReport) revertedReplyFromObservedState(info TransmissionInfo, errorMsg string) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{
		TxStatus: stellarcap.TxStatus_TX_STATUS_REVERTED,
	}
	status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
	reply.ReceiverContractExecutionStatus = &status
	if info.LedgerSequence != 0 {
		reply.LedgerSequence = capcommon.Ptr(info.LedgerSequence)
	}
	if errorMsg != "" {
		reply.ErrorMessage = capcommon.Ptr(errorMsg)
	}
	return reply
}

// replyFromOwnTransaction builds a WriteReportReply directly from a SubmitTransactionResponse
// when the post-submit transmission info poll fails or returns an unexpected state.
func (wr *writeReport) replyFromOwnTransaction(resp *stellartypes.SubmitTransactionResponse) *stellarcap.WriteReportReply {
	reply := &stellarcap.WriteReportReply{}
	if resp == nil {
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_FATAL
		return reply
	}
	populateReplyFromSubmit(reply, resp)
	switch resp.TxStatus {
	case stellartypes.TxSuccess:
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_SUCCESS
		status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		reply.ReceiverContractExecutionStatus = &status
	case stellartypes.TxFailed:
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_REVERTED
		status := stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
		reply.ReceiverContractExecutionStatus = &status
		if resp.Error != "" {
			reply.ErrorMessage = capcommon.Ptr("on-chain transaction failed: " + resp.Error)
		}
	default: // TxFatal
		reply.TxStatus = stellarcap.TxStatus_TX_STATUS_FATAL
	}
	return reply
}

// populateReplyFromSubmit sets tx hash, fee, and block timestamp on the reply from a SubmitTransactionResponse.
// Ledger sequence on submit paths is populated from get_transmission_info (post-submit poll), not from ResultMetaXDR.
func populateReplyFromSubmit(reply *stellarcap.WriteReportReply, resp *stellartypes.SubmitTransactionResponse) {
	if resp == nil {
		return
	}
	if resp.TxHash != "" {
		reply.TxHash = capcommon.Ptr(resp.TxHash)
	}
	if resp.TransactionFee != nil {
		reply.TransactionFee = resp.TransactionFee
	}
	if resp.BlockTimestamp != nil {
		reply.BlockTimestamp = resp.BlockTimestamp
	}
}
