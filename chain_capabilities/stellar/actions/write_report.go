package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
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
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

const (
	ocrSignatureLen = 65

	unknownIssueExecutingReceiverContractMessage       = "receiver contract execution failed"
	writeReportUnexpectedSuccessfulTransmissionMessage = "WriteReport unexpected successful transmission"
)

type writeReport struct {
	service                  types.StellarService
	forwarderClient          CREForwarderClient
	lggr                     logger.SugaredLogger
	forwarderLookbackLedgers int64
	chainSelector            uint64
	reportSizeLimit          limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler    ts.TransmissionScheduler
	messageBuilder           *monitoring.MessageBuilder
	beholderProcessor        beholder.ProtoProcessor
}

func (s *Stellar) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *stellarcap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}
	monitoring.EmitInitiated(ctx, s.lggr, s.beholderProcessor, s.messageBuilder.BuildWriteReportInitiated(telemetryContext, input))

	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		capErr := caperrors.NewPublicUserError(err, caperrors.InvalidArgument)
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport, user error due to invalid request", capErr))
		return nil, capErr
	}

	reply, meteringMeta, err := s.executeWriteReport(ctx, input, metadata, telemetryContext)
	if err != nil {
		isUserError := s.isUserErrorWriteReport(err)
		capErr := capcommon.GetError(err, isUserError)
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport while checking if the report exists or trying to publish on chain", capErr))
		return nil, capErr
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully executed WriteReport", s.lggr, s.beholderProcessor,
		s.messageBuilder.BuildWriteReportSuccess(telemetryContext, input))

	return &capabilities.ResponseAndMetadata[*stellarcap.WriteReportReply]{
		Response:         reply,
		ResponseMetadata: meteringMeta,
	}, nil
}

func (s *Stellar) executeWriteReport(
	ctx context.Context,
	request *stellarcap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
	telemetryContext monitoring.TelemetryContext,
) (*stellarcap.WriteReportReply, capabilities.ResponseMetadata, error) {
	wr := &writeReport{
		service:                  s.StellarService,
		forwarderClient:          s.forwarderClient,
		lggr:                     s.messageBuilder.RequestLggr(s.lggr, telemetryContext),
		forwarderLookbackLedgers: s.forwarderLookbackLedgers,
		chainSelector:            s.chainSelector,
		reportSizeLimit:          s.reportSizeLimit,
		transmissionScheduler:    s.transmissionScheduler,
		messageBuilder:           s.messageBuilder,
		beholderProcessor:        s.beholderProcessor,
	}
	return wr.execute(ctx, request, metadata, telemetryContext)
}

func (wr *writeReport) execute(
	ctx context.Context,
	request *stellarcap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
	telemetryContext monitoring.TelemetryContext,
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

	// TODO(follow-up): Consider simulating the on_report transaction before polling when the
	// transmission has not yet been attempted. If simulation predicts a terminal failure we
	// could skip delta-stage polling and/or submission. Open questions: simulation may fail on
	// only some DON nodes (stale ledger, timing), so every node must still return the same
	// WriteReportReply and metering semantics before enabling this shortcut.
	info, err := wr.pollTransmissionInfo(ctx, request, telemetryContext, transmissionID, queuePosition)
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
		reply, err := wr.buildSuccessReply(ctx, request, telemetryContext, txHash)
		return reply, capabilities.ResponseMetadata{}, err
	case TransmissionStateInvalidReceiver:
		txHash, hashErr := txHashRetriever.GetFailedTransmissionHash(ctx)
		if hashErr != nil {
			if errors.Is(hashErr, ErrUnexpectedSuccessfulTransmission) {
				wr.emitInvalidTransmissionState(ctx, request, telemetryContext, info, transmissionID, writeReportUnexpectedSuccessfulTransmissionMessage, hashErr.Error())
			} else {
				wr.lggr.Errorw("Returning without a transmission attempt - prior transmission marked receiver invalid, but failed to retrieve its tx hash", "error", hashErr)
			}
			return nil, capabilities.ResponseMetadata{}, hashErr
		}
		reply, err := wr.buildRevertReplyFromTx(ctx, request, telemetryContext, txHash, info, transmissionID)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, revertReplyBuildError(info, transmissionID, err)
		}
		return reply, capabilities.ResponseMetadata{}, nil
	case TransmissionStateFailed:
		txHash, hashErr := txHashRetriever.GetFailedTransmissionHash(ctx)
		if hashErr != nil {
			if errors.Is(hashErr, ErrUnexpectedSuccessfulTransmission) {
				wr.emitInvalidTransmissionState(ctx, request, telemetryContext, info, transmissionID, writeReportUnexpectedSuccessfulTransmissionMessage, hashErr.Error())
			} else {
				wr.lggr.Errorw("Returning without a transmission attempt - prior transmission failed, but failed to retrieve its tx hash", "error", hashErr)
			}
			return nil, capabilities.ResponseMetadata{}, hashErr
		}
		reply, err := wr.buildRevertReplyFromTx(ctx, request, telemetryContext, txHash, info, transmissionID)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, revertReplyBuildError(info, transmissionID, err)
		}
		return reply, capabilities.ResponseMetadata{}, nil
	case TransmissionStateNotAttempted:
	default:
		wr.emitInvalidTransmissionState(ctx, request, telemetryContext, info, transmissionID, "WriteReport invalid transmission state during pre-submit poll", invalidTransmissionStateError(info.State).Error())
		return nil, capabilities.ResponseMetadata{}, invalidTransmissionStateError(info.State)
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
		// Transmission info may lag even when ReportProcessed events are already indexed (e.g. duplicate
		// submit where another node's tx succeeded). Prefer the canonical event hash over local TXM data.
		wr.lggr.Warnw("Failed to poll transmission info after submit, attempting event-based tx hash lookup", "error", pollErr)
		if txHash, lookupErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx); lookupErr == nil {
			reply, buildErr := wr.buildSuccessReply(ctx, request, telemetryContext, txHash)
			return reply, wr.meteringFromReply(reply), buildErr
		}
		wr.lggr.Warnw("Failed to poll transmission info after submit, returning reply from TXM outcome", "error", pollErr)
		reply := wr.replyFromOwnTransaction(submitResp)
		return reply, wr.meteringFromReply(reply), nil
	}

	switch postInfo.State {
	case TransmissionStateSucceeded:
		txHash, err := txHashRetriever.GetSuccessfulTransmissionHash(ctx)
		if err != nil {
			return nil, capabilities.ResponseMetadata{}, err
		}
		if submitResp.TxStatus != stellartypes.TxSuccess && submitResp.TxHash != "" && submitResp.TxHash != txHash {
			monitoring.LogAndEmitSuccess(ctx, "Made a new transmission attempt - transmission succeeded, but local submit did not confirm (likely duplicate)",
				wr.lggr, wr.beholderProcessor,
				wr.messageBuilder.BuildWriteReportDuplicateTx(telemetryContext, request, submitResp.TxHash, txHash))
		}
		reply, err := wr.buildSuccessReply(ctx, request, telemetryContext, txHash)
		return reply, wr.meteringFromReply(reply), err
	case TransmissionStateFailed, TransmissionStateInvalidReceiver:
		txHash, err := txHashRetriever.GetFailedTransmissionHash(ctx)
		if err != nil {
			if errors.Is(err, ErrUnexpectedSuccessfulTransmission) {
				wr.emitInvalidTransmissionState(ctx, request, telemetryContext, postInfo, transmissionID, writeReportUnexpectedSuccessfulTransmissionMessage, err.Error())
			} else {
				wr.lggr.Errorw("Made a new transmission attempt - transmission failed, unable to retrieve failed transmission tx hash", "error", err, "localTxHash", submitResp.TxHash)
			}
			return nil, capabilities.ResponseMetadata{}, err
		}
		if submitResp.TxHash != "" && submitResp.TxHash != txHash {
			monitoring.LogAndEmitSuccess(ctx, "Made a new transmission attempt - transmission failed, but local submit hash differs from canonical failed transmission",
				wr.lggr, wr.beholderProcessor,
				wr.messageBuilder.BuildWriteReportDuplicateTx(telemetryContext, request, submitResp.TxHash, txHash))
		}
		wr.lggr.Errorw("Made a new transmission attempt - transmission failed", "txHash", txHash, "transmissionState", postInfo.State)
		reply, err := wr.buildRevertReplyFromTx(ctx, request, telemetryContext, txHash, postInfo, transmissionID)
		if err != nil {
			return nil, wr.meteringFromReply(reply), revertReplyBuildError(postInfo, transmissionID, err)
		}
		return reply, wr.meteringFromReply(reply), nil
	default:
		wr.lggr.Errorw("Invalid transmission state after submit", "state", postInfo.State, "localTxStatus", submitResp.TxStatus)
		wr.emitInvalidTransmissionState(ctx, request, telemetryContext, postInfo, transmissionID, "WriteReport invalid transmission state after submit", invalidTransmissionStateError(postInfo.State).Error())
		return nil, capabilities.ResponseMetadata{}, invalidTransmissionStateError(postInfo.State)
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
	request *stellarcap.WriteReportRequest,
	telemetryContext monitoring.TelemetryContext,
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
	deltaStagePassed := false
	hadSuccessfulPoll := false
	// Guard so an unexpected state that persists across multiple poll iterations only
	// emits one InvalidTransmissionState metric, not one per poll tick.
	invalidStateEmitted := false
	defer func() {
		stageTimer.Stop()
		if wr.monitoringEnabled() && !deltaStagePassed && hadSuccessfulPoll {
			monitoring.LogAndEmitSuccess(ctx, "Transmission found before delta stage has passed",
				wr.lggr, wr.beholderProcessor,
				wr.messageBuilder.BuildWriteReportSuccessfulEarlyReturn(telemetryContext))
		}
	}()

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
				if !invalidStateEmitted {
					wr.emitInvalidTransmissionState(ctx, request, telemetryContext, lastValidInfo, transmissionID,
						"Unexpected transmission state; continuing to poll",
						invalidTransmissionStateError(lastValidInfo.State).Error())
					invalidStateEmitted = true
				}
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
			deltaStagePassed = true
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

func invalidTransmissionStateError(state TransmissionState) error {
	return fmt.Errorf("unexpected transmission state: %d", state)
}

func (wr *writeReport) buildSuccessReply(
	ctx context.Context,
	request *stellarcap.WriteReportRequest,
	telemetryContext monitoring.TelemetryContext,
	txHash string,
) (*stellarcap.WriteReportReply, error) {
	return wr.replyFromTransaction(ctx, request, telemetryContext, txHash, stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS, nil)
}

func (wr *writeReport) buildRevertReplyFromTx(
	ctx context.Context,
	request *stellarcap.WriteReportRequest,
	telemetryContext monitoring.TelemetryContext,
	txHash string,
	transmissionInfo TransmissionInfo,
	transmissionID TransmissionID,
) (*stellarcap.WriteReportReply, error) {
	errorMessage := revertReason(transmissionInfo, transmissionID)
	return wr.replyFromTransaction(ctx, request, telemetryContext, txHash, stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED, &errorMessage)
}

func revertReason(transmissionInfo TransmissionInfo, transmissionID TransmissionID) string {
	if transmissionInfo.State == TransmissionStateInvalidReceiver {
		return transmissionID.InvalidReceiverMessage()
	}
	return unknownIssueExecutingReceiverContractMessage
}

func revertReplyBuildError(transmissionInfo TransmissionInfo, transmissionID TransmissionID, err error) error {
	return fmt.Errorf("%s %s: this is the root cause, but an additional error occurred while fetching more info: %w", capcommon.UserError, revertReason(transmissionInfo, transmissionID), err)
}

func (wr *writeReport) replyFromTransaction(
	ctx context.Context,
	request *stellarcap.WriteReportRequest,
	telemetryContext monitoring.TelemetryContext,
	txHash string,
	receiverStatus stellarcap.ReceiverContractExecutionStatus,
	errorMessage *string,
) (*stellarcap.WriteReportReply, error) {
	txResp, err := capcommon.WithQuickRetry(ctx, wr.lggr, func(ctx context.Context) (stellartypes.GetTransactionResponse, error) {
		return wr.service.GetTransaction(ctx, stellartypes.GetTransactionRequest{TxHash: txHash})
	})
	if err != nil {
		if wr.monitoringEnabled() {
			monitoring.LogAndEmitError(ctx, wr.lggr, wr.beholderProcessor,
				wr.messageBuilder.BuildWriteReportTxInfoRetrievalError(telemetryContext, request, txHash, err.Error()))
		}
		return nil, fmt.Errorf("failed to get transaction for tx hash %s: %w", txHash, err)
	}

	message := errorMessage
	if receiverStatus == stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED && errorMessage == nil {
		message = capcommon.Ptr(unknownIssueExecutingReceiverContractMessage)
	}

	txStatus := stellarcap.TxStatus_TX_STATUS_SUCCESS
	if receiverStatus == stellarcap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED {
		txStatus = stellarcap.TxStatus_TX_STATUS_REVERTED
	}

	reply := &stellarcap.WriteReportReply{
		TxHash:                          capcommon.Ptr(txHash),
		TxStatus:                        txStatus,
		ReceiverContractExecutionStatus: &receiverStatus,
		ErrorMessage:                    message,
	}
	if txResp.FeeStroops > 0 {
		fee := txResp.FeeStroops
		reply.TransactionFee = &fee
	}
	if txResp.LedgerCloseTime > 0 {
		blockTimestamp := uint64(txResp.LedgerCloseTime) * 1_000_000
		reply.BlockTimestamp = &blockTimestamp
	}
	if txResp.LedgerSequence > 0 {
		reply.LedgerSequence = capcommon.Ptr(txResp.LedgerSequence)
	}
	return reply, nil
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

func transmissionDebugID(id TransmissionID) string {
	return fmt.Sprintf("%s:%s:%s", id.Receiver, id.ReportIDHex(), id.WorkflowExecutionIDHex())
}

func (wr *writeReport) monitoringEnabled() bool {
	return wr.messageBuilder != nil && wr.beholderProcessor != nil
}

func (wr *writeReport) emitInvalidTransmissionState(
	ctx context.Context,
	request *stellarcap.WriteReportRequest,
	telemetryContext monitoring.TelemetryContext,
	info TransmissionInfo,
	transmissionID TransmissionID,
	summary, cause string,
) {
	if !wr.monitoringEnabled() {
		return
	}
	monitoring.LogAndEmitError(ctx, wr.lggr, wr.beholderProcessor,
		wr.messageBuilder.BuildWriteReportInvalidTransmissionState(
			telemetryContext,
			request,
			uint32(info.State),
			info.InvalidReceiver,
			info.Success,
			transmissionDebugID(transmissionID),
			info.Transmitter,
			summary,
			cause,
		))
}
