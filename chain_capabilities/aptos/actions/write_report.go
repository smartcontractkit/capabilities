package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strings"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
)

// ForwarderGasOverhead is the gas consumed by the forwarder contract before dispatching
// to the receiver. Transactions with MaxGasAmount below this will fail before
// reaching the receiver.
// Measured at ~2,093 gas units on Aptos testnet (31 oracles, f=10, 5 KB payload).
// Set to 2x with margin for gas schedule changes.
const ForwarderGasOverhead uint64 = 4000

// aptosOctasToAPT converts a fee in octas (10^-8 APT) to a *big.Float in APT.
func aptosOctasToAPT(octas uint64) *big.Float {
	return new(big.Float).Quo(new(big.Float).SetUint64(octas), big.NewFloat(1e8))
}

// WriteReport validates and submits a signed report to the Aptos chain via the CRE forwarder.
func (s *Aptos) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}
	monitoring.EmitInitiated(ctx, s.lggr, s.beholderProcessor, s.messageBuilder.BuildWriteReportInitiated(telemetryContext, input))

	// 1. Validate inputs
	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport, user error due to invalid request", err.Error(), true))
		return nil, capcommon.NewUserError(err)
	}
	s.lggr.Debugw("Inputs validated successfully")

	// 2. Build and submit the transaction via AptosService
	reply, meteringMetadata, err := s.executeWriteReport(ctx, input, metadata, telemetryContext)
	if err != nil {
		isUserError := s.isUserError(err)
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildWriteReportError(telemetryContext, input, "Failed to WriteReport while checking if the report exists or trying to publish on chain", err.Error(), isUserError))
		return nil, capcommon.GetError(err, isUserError)
	}

	monitoring.LogAndEmitSuccess(ctx, "Successful WriteReport execution", s.lggr, s.beholderProcessor,
		s.messageBuilder.BuildWriteReportSuccess(telemetryContext, input))

	return &capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply]{
		Response:         reply,
		ResponseMetadata: meteringMetadata,
	}, nil
}

type writeReport struct {
	forwarderClient                 CREForwarderClient
	forwarderAddress                aptos_sdk.AccountAddress
	aptosService                    types.AptosService
	lggr                            logger.SugaredLogger
	p2pConfig                       map[string]string
	chainSelector                   uint64
	maxGasAmountLimit               limits.BoundLimiter[uint64]
	reportSizeLimit                 limits.BoundLimiter[commoncfg.Size]
	writeReportBlockTimestampActive limits.RangeLimiter[commoncfg.Timestamp]
	executionTimestamp              time.Time
	transmissionScheduler           ts.TransmissionScheduler
	txSearchStartingBuffer          time.Duration
	beholderProcessor               beholder.ProtoProcessor
	messageBuilder                  *monitoring.MessageBuilder
}

func (s *Aptos) executeWriteReport(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
	telemetryContext monitoring.TelemetryContext,
) (*aptoscap.WriteReportReply, capabilities.ResponseMetadata, error) {
	wr := &writeReport{
		forwarderClient:                 s.forwarderClient,
		forwarderAddress:                s.forwarderAddress,
		aptosService:                    s.AptosService,
		lggr:                            s.messageBuilder.RequestLggr(s.lggr, telemetryContext),
		p2pConfig:                       s.p2pConfig,
		chainSelector:                   s.chainSelector,
		maxGasAmountLimit:               s.maxGasAmountLimit,
		reportSizeLimit:                 s.reportSizeLimit,
		writeReportBlockTimestampActive: s.writeReportBlockTimestampActive,
		executionTimestamp:              metadata.ExecutionTimestamp,
		transmissionScheduler:           s.transmissionScheduler,
		txSearchStartingBuffer:          s.txSearchStartingBuffer,
		beholderProcessor:               s.beholderProcessor,
		messageBuilder:                  s.messageBuilder,
	}
	return wr.execute(ctx, request, metadata, telemetryContext)
}

// TODO: handle gas limit bumping if required (PLEX-2580)
func (wr *writeReport) execute(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
	telemetryContext monitoring.TelemetryContext,
) (*aptoscap.WriteReportReply, capabilities.ResponseMetadata, error) {
	wr.lggr.Debugw("Execute started",
		"workflowExecutionID", metadata.WorkflowExecutionID,
		"hasGasConfig", request.GasConfig != nil,
		"reportLen", len(request.Report.RawReport),
		"numSigs", len(request.Report.Sigs),
		"receiver", hex.EncodeToString(request.Receiver[:]),
	)
	ctx = contexts.WithChainSelector(ctx, wr.chainSelector)
	// this helps the node query only relevant transactions when trying to find the txHash,
	// anything before (requestStartTime - txSearchStartingBuffer) is not relevant
	requestStartTime := time.Now()

	// Set gas limits: use defaults if not provided, otherwise check against configured limit
	if request.GasConfig == nil {
		request.GasConfig = &aptoscap.GasConfig{}
		limit, limErr := wr.maxGasAmountLimit.Limit(ctx)
		if limErr != nil {
			wr.lggr.Errorw("Failed to get gas limit", "error", limErr)
			return nil, capabilities.ResponseMetadata{}, limErr
		}
		request.GasConfig.MaxGasAmount = limit
		wr.lggr.Debugw("Using default gas limit", "maxGasAmount", limit)
	} else {
		err := wr.maxGasAmountLimit.Check(ctx, request.GasConfig.MaxGasAmount)
		if err != nil {
			wr.lggr.Errorw("Gas config exceeds limit", "maxGasAmount", request.GasConfig.MaxGasAmount, "error", err)
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s provided gas config exceeds limit (maxGasAmount=%d): %w", capcommon.UserError, request.GasConfig.MaxGasAmount, err)
		}
		wr.lggr.Debugw("Using provided gas config", "maxGasAmount", request.GasConfig.MaxGasAmount)
	}

	if request.GasConfig.MaxGasAmount < ForwarderGasOverhead {
		wr.lggr.Errorw("MaxGasAmount below forwarder overhead, transaction would fail before reaching receiver",
			"maxGasAmount", request.GasConfig.MaxGasAmount, "forwarderGasOverhead", ForwarderGasOverhead)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s MaxGasAmount (%d) is below the forwarder gas overhead (%d), transaction would fail before reaching the receiver contract",
			capcommon.UserError, request.GasConfig.MaxGasAmount, ForwarderGasOverhead)
	}

	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		wr.lggr.Errorw("GetTransmissionID failed", "error", err)
		return nil, capabilities.ResponseMetadata{}, err
	}
	wr.lggr = wr.lggr.With("transmissionID", transmissionID.GetDebugID())

	txInfoRetriever := NewTxInfoRetriever(wr.forwarderClient, wr.lggr, transmissionID, wr.forwarderAddress.String(), requestStartTime, wr.txSearchStartingBuffer, request.Report,
		WithTxInfoRetrieverMonitoring(wr.beholderProcessor, wr.messageBuilder, telemetryContext))

	queuePosition := wr.transmissionScheduler.GetQueuePosition(transmissionID.GetDebugID())
	orderedTransmitters := wr.getOrderedTransmitters(transmissionID.GetDebugID(), wr.p2pConfig)
	wr.lggr.Debugw("Got queue position", "queuePosition", queuePosition, "orderedTransmitters", orderedTransmitters)
	// polling here is done based on queue position and deltaStage
	transmissionInfo, err := wr.pollTransmissionInfo(ctx, transmissionID, queuePosition, telemetryContext)
	if err != nil {
		wr.lggr.Errorw("PollTransmissionInfo failed", "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get transmission info: %w", err)
	}
	wr.lggr.Debugw("Initial pollTransmissionInfo result", "success", transmissionInfo.Success, "transmitter", transmissionInfo.Transmitter.String())

	if transmissionInfo.Success {
		wr.logIfSuccessfulTransmitterNotInOrderedList(ctx, telemetryContext, transmissionInfo, orderedTransmitters)
		wr.lggr.Debugw("Report already onchain, retrieving txHash")
		txResult, txHashErr := txInfoRetriever.GetSuccessfulTransmissionInfo(ctx, transmissionInfo.Transmitter)
		if txHashErr != nil {
			wr.lggr.Errorw("Report already onchain but failed to retrieve its txHash", "error", txHashErr)
			return nil, capabilities.ResponseMetadata{}, txHashErr
		}
		feeOctas := txResult.GasUsed * txResult.GasUnitPrice
		receiverContractExecutionStatus := aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS
		reply := &aptoscap.WriteReportReply{
			TxStatus:                        aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:                          &txResult.TxHash,
			ReceiverContractExecutionStatus: &receiverContractExecutionStatus,
			TransactionFee:                  &feeOctas,
			BlockTimestamp:                  wr.maybeBlockTimestamp(ctx, txResult.BlockTimestamp),
		}
		return reply, capabilities.ResponseMetadata{}, nil
	}

	err = wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		wr.lggr.Errorw("Report size exceeds limit", "reportSize", len(request.Report.RawReport), "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s report size exceeds limit: %w", capcommon.UserError, err)
	}

	wr.lggr.Debugw("Submitting WriteReport transaction", "executionID", metadata.WorkflowExecutionID, "receiver", hex.EncodeToString(request.Receiver[:]))

	invokeOnReportStart := time.Now()
	txReply, err := wr.forwarderClient.InvokeOnReport(ctx, request.Receiver, request.Report, request.GasConfig)
	if err != nil {
		wr.lggr.Errorw("InvokeOnReport failed", "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to invoke forwarder report: %w", err)
	}
	invokeOnReportDuration := time.Since(invokeOnReportStart)
	monitoring.EmitInitiated(ctx, wr.lggr, wr.beholderProcessor, wr.messageBuilder.BuildWriteReportInvokeOnReportDuration(
		telemetryContext,
		int64(math.Max(float64(invokeOnReportDuration.Milliseconds()), 0)),
		int32(txReply.TxStatus), //nolint:gosec // txReply.TxStatus is a small enum value: 0, 1, 2.
	))

	wr.lggr.Debugw("InvokeOnReport returned", "txHash", txReply.TxHash, "txStatus", txReply.TxStatus, "txIdempotencyKey", txReply.TxIdempotencyKey)

	// Resolve V_ours up-front so the post-submit forwarder read pins to it; reading at >=V_ours
	// avoids stale-fullnode false negatives.
	var ownMeteringMetadata capabilities.ResponseMetadata
	var pinnedLedgerVersion *uint64
	ownLedgerVersion, ownFeeInOctas, ownVMStatus, ownBlockTimestamp, feeErr := wr.getTxnInfoFromChain(ctx, txReply.TxHash)
	if feeErr != nil {
		wr.lggr.Errorw("Failed to get transaction fee, using zero for metering", "txHash", txReply.TxHash, "error", feeErr)
		ownMeteringMetadata = metering.GetResponseMetadataWriteReport(big.NewFloat(0), wr.chainSelector)
		monitoring.LogAndEmitError(ctx, wr.lggr, wr.beholderProcessor,
			wr.messageBuilder.BuildWriteReportTxFeeCalculationError(telemetryContext, request, txReply.TxHash, feeErr.Error()))
	} else {
		pinnedLedgerVersion = &ownLedgerVersion
		feeInAPT := aptosOctasToAPT(ownFeeInOctas)
		ownMeteringMetadata = metering.GetResponseMetadataWriteReport(feeInAPT, wr.chainSelector)
		wr.lggr.Debugw("WriteReport fee", "feeInAPT", feeInAPT.String(), "feeInOctas", ownFeeInOctas, "ledgerVersion", ownLedgerVersion)
	}

	newTransmissionInfo, err := capcommon.WithPollingRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readTransmissionInfo, readTransmissionErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID, pinnedLedgerVersion)
		if readTransmissionErr != nil {
			return TransmissionInfo{}, readTransmissionErr
		}
		return readTransmissionInfo, nil
	})

	if err != nil {
		wr.lggr.Errorw("Post-submission polling failed", "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed getting transmission info after node submitted the report on chain, %w", err)
	}

	wr.lggr.Debugw("Post-submission transmission status", "success", newTransmissionInfo.Success, "transmitter", newTransmissionInfo.Transmitter.String())

	switch newTransmissionInfo.Success {
	case true:
		wr.logIfSuccessfulTransmitterNotInOrderedList(ctx, telemetryContext, newTransmissionInfo, orderedTransmitters)
		receiverContractExecutionStatus := aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_SUCCESS

		switch txReply.TxStatus {
		case aptostypes.TxReverted, aptostypes.TxFatal:
			// Report for this transaction has already been submitted and we sent a duplicate tx onchain, that is why this tx reverted but transmission info still shows success.
			successResult, txHashErr := txInfoRetriever.GetSuccessfulTransmissionInfo(ctx, newTransmissionInfo.Transmitter)
			if txHashErr != nil {
				wr.lggr.Errorw("Failed to get successful transmission hash", "error", txHashErr)
				return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get successful transmission hash: %w", txHashErr)
			}
			feeInOctas := successResult.GasUsed * successResult.GasUnitPrice
			feeInAPT := aptosOctasToAPT(feeInOctas)
			meteringMetadata := metering.GetResponseMetadataWriteReport(feeInAPT, wr.chainSelector)
			wr.lggr.Debugw("WriteReport fee", "feeInAPT", feeInAPT.String(), "feeInOctas", feeInOctas)

			monitoring.LogAndEmitSuccess(ctx, "WriteReport sent a duplicate transaction, report already on-chain",
				wr.lggr, wr.beholderProcessor,
				wr.messageBuilder.BuildWriteReportDuplicateTx(telemetryContext, request, txReply.TxHash, successResult.TxHash))

			if txReply.TxStatus == aptostypes.TxFatal {
				wr.lggr.Errorw("Transaction failed to get processed, but report was already submitted, this is unexpected and should be investigated", "txHash", txReply.TxHash)
				meteringMetadata = metering.GetResponseMetadataWriteReport(big.NewFloat(0), wr.chainSelector)
			}

			return &aptoscap.WriteReportReply{
				TxStatus:                        aptoscap.TxStatus_TX_STATUS_SUCCESS,
				TxHash:                          &successResult.TxHash,
				TransactionFee:                  &feeInOctas,
				ReceiverContractExecutionStatus: &receiverContractExecutionStatus,
				BlockTimestamp:                  wr.maybeBlockTimestamp(ctx, successResult.BlockTimestamp),
			}, meteringMetadata, nil
		case aptostypes.TxSuccess:
			return &aptoscap.WriteReportReply{
				TxStatus:                        aptoscap.TxStatus_TX_STATUS_SUCCESS,
				TxHash:                          &txReply.TxHash,
				TransactionFee:                  &ownFeeInOctas,
				ReceiverContractExecutionStatus: &receiverContractExecutionStatus,
				BlockTimestamp:                  wr.maybeBlockTimestamp(ctx, ownBlockTimestamp),
			}, ownMeteringMetadata, nil
		default:
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("unexpected tx status: %v", txReply.TxStatus)
		}
	case false:
		if txReply.TxStatus == aptostypes.TxSuccess {
			wr.lggr.Errorw("Unexpected state - local tx succeeded but transmission info shows no success",
				"transmissionID", transmissionID.GetDebugID())
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("unexpected state: local transaction succeeded but transmission info shows no success for %s", transmissionID.GetDebugID())
		}

		ownReply := &aptoscap.WriteReportReply{
			TxStatus:                        aptoscap.TxStatus_TX_STATUS_FATAL,
			TxHash:                          &txReply.TxHash,
			TransactionFee:                  &ownFeeInOctas,
			ErrorMessage:                    ptrIfNonEmpty(ownVMStatus),
			ReceiverContractExecutionStatus: receiverContractExecutionStatusFromFailedVMStatus(ownVMStatus, wr.forwarderAddress),
			BlockTimestamp:                  wr.maybeBlockTimestamp(ctx, ownBlockTimestamp),
		}
		// Position 0 node has no prior nodes to check; return its own failed tx hash.
		if queuePosition <= 0 {
			wr.lggr.Debugw("Position 0, returning own failed hash", "txHash", txReply.TxHash, "vmStatus", ownVMStatus)
			return ownReply, ownMeteringMetadata, nil
		}

		// Search preceding transmitters (position 0 through position-1) for a matching failed tx.
		for i := 0; i < queuePosition && i < len(orderedTransmitters); i++ {
			if orderedTransmitters[i] == "" {
				wr.lggr.Warnw("Skipping empty transmitter address, p2pConfig is incomplete", "index", i)
				monitoring.EmitInitiated(ctx, wr.lggr, wr.beholderProcessor,
					wr.messageBuilder.BuildWriteReportP2pConfigIncomplete(telemetryContext, int32(i))) //nolint:gosec // i is a small queue index
				continue
			}
			wr.lggr.Debugw("Checking prior transmitter", "index", i, "address", orderedTransmitters[i])
			addr, err := aptos_sdk.ConvertToAddress(orderedTransmitters[i])
			if err != nil {
				wr.lggr.Errorw("Failed to convert transmitter address to address", "address", orderedTransmitters[i], "error", err)
				continue
			}
			failedResult, searchErr := txInfoRetriever.GetFailedTransmissionInfo(ctx, *addr)
			if searchErr != nil {
				wr.lggr.Debugw("No matching failed tx for prior transmitter", "transmitter", orderedTransmitters[i], "position", i, "err", searchErr)
				continue
			}
			wr.lggr.Debugw("Found failed transmission from prior node", "transmitter", orderedTransmitters[i], "position", i, "txHash", failedResult.TxHash, "vmStatus", failedResult.VMStatus)
			feeOctas := failedResult.GasUsed * failedResult.GasUnitPrice
			recvStatus := receiverContractExecutionStatusFromFailedVMStatus(failedResult.VMStatus, wr.forwarderAddress)

			// charge the user for the failed txs if it was reverted on user contract
			var replyMeta capabilities.ResponseMetadata
			if recvStatus != nil && *recvStatus == aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED {
				feeInAPT := aptosOctasToAPT(feeOctas)
				replyMeta = metering.GetResponseMetadataWriteReport(feeInAPT, wr.chainSelector)
			}
			return &aptoscap.WriteReportReply{
				TxStatus:                        aptoscap.TxStatus_TX_STATUS_FATAL,
				TxHash:                          &failedResult.TxHash,
				TransactionFee:                  &feeOctas,
				ErrorMessage:                    ptrIfNonEmpty(failedResult.VMStatus),
				ReceiverContractExecutionStatus: recvStatus,
				BlockTimestamp:                  wr.maybeBlockTimestamp(ctx, failedResult.BlockTimestamp),
			}, replyMeta, nil
		}

		// No matching failed tx from prior nodes; return our own hash.
		wr.lggr.Debugw("No prior failed tx found, returning own hash", "txHash", txReply.TxHash, "vmStatus", ownVMStatus)
		return ownReply, ownMeteringMetadata, nil
	}
	return nil, capabilities.ResponseMetadata{}, nil // should never happen
}

// buildPreSubmissionFatalReply evaluates node 0's failed tx and returns a fatal reply
// if we should NOT submit, or (nil, empty) if we should proceed to submit.
func (wr *writeReport) buildPreSubmissionFatalReply(failedResult TransmissionTxInfo, ourMaxGasAmount uint64) (*aptoscap.WriteReportReply, capabilities.ResponseMetadata) {
	if isOutOfGas(failedResult.VMStatus) && ourMaxGasAmount > failedResult.MaxGasAmount {
		// We have more gas headroom than node 0 — proceed to submit.
		return nil, capabilities.ResponseMetadata{}
	}

	// Either not OOG (user error, unrecoverable) or our gas isn't higher — return fatal.
	// No metering: we never submitted, so we incurred no gas cost.
	feeOctas := failedResult.GasUsed * failedResult.GasUnitPrice
	return &aptoscap.WriteReportReply{
		TxStatus:                        aptoscap.TxStatus_TX_STATUS_FATAL,
		TxHash:                          &failedResult.TxHash,
		TransactionFee:                  &feeOctas,
		ErrorMessage:                    ptrIfNonEmpty(failedResult.VMStatus),
		ReceiverContractExecutionStatus: receiverContractExecutionStatusFromFailedVMStatus(failedResult.VMStatus, wr.forwarderAddress),
	}, capabilities.ResponseMetadata{}
}

// isOutOfGas returns true if the VM status string indicates the transaction
// exhausted its total gas budget. The Aptos VM returns exactly "Out of gas".
func isOutOfGas(vmStatus string) bool {
	return vmStatus == "Out of gas"
}

// getTxnInfoFromChain returns the committed ledger version, fee in octas (gasUsed * gasUnitPrice),
// and VM status for a submitted tx, looked up via AptosService.TransactionByHash.
func (wr *writeReport) getTxnInfoFromChain(ctx context.Context, txHash string) (uint64, uint64, string, uint64, error) {
	reply, err := capcommon.WithQuickRetry(ctx, wr.lggr, func(ctx context.Context) (*aptostypes.TransactionByHashReply, error) {
		return wr.aptosService.TransactionByHash(ctx, aptostypes.TransactionByHashRequest{Hash: txHash})
	})
	if err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to get transaction by hash: %w", err)
	}
	if reply == nil || reply.Transaction == nil {
		return 0, 0, "", 0, fmt.Errorf("transaction by hash %s returned empty reply", txHash)
	}
	if reply.Transaction.Version == nil {
		return 0, 0, "", 0, fmt.Errorf("transaction %s has no committed ledger version", txHash)
	}
	if reply.Transaction.Data == nil {
		return *reply.Transaction.Version, 0, "", 0, fmt.Errorf("transaction %s has nil data", txHash)
	}
	var txData userTxData
	if err := json.Unmarshal(reply.Transaction.Data, &txData); err != nil {
		return 0, 0, "", 0, fmt.Errorf("failed to unmarshal transaction data: %w", err)
	}
	if txData.Timestamp < 0 {
		wr.lggr.Warnw("Invalid negative timestamp, skipping timestamp", "txHash", txHash, "timestamp", txData.Timestamp)
		return *reply.Transaction.Version, txData.GasUsed * txData.GasUnitPrice, txData.VMStatus, 0, nil
	}
	return *reply.Transaction.Version, txData.GasUsed * txData.GasUnitPrice, txData.VMStatus, uint64(txData.Timestamp), nil
}

func (wr *writeReport) includeBlockTimestampInReply(ctx context.Context) bool {
	if wr.writeReportBlockTimestampActive == nil {
		return false
	}
	if wr.executionTimestamp.IsZero() {
		wr.lggr.Errorw("ExecutionTimestamp is zero")
	}
	return wr.writeReportBlockTimestampActive.Check(ctx, commoncfg.NewTimestamp(wr.executionTimestamp)) == nil
}

func (wr *writeReport) maybeBlockTimestamp(ctx context.Context, ts uint64) *uint64 {
	if !wr.includeBlockTimestampInReply(ctx) {
		wr.lggr.Debugw("WriteReport block timestamp feature flag is inactive; omitting block_timestamp from reply")
		return nil
	}
	return &ts
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

const moveAbortInPrefix = "move abort in "

// receiverContractExecutionStatusFromFailedVmStatus sets REVERTED when vmStatus is an Aptos
// "Move abort in 0x<addr>::<module>: ..." and the aborting module account is not the CRE forwarder.
// Forwarder-side aborts leave the field unset (nil).
func receiverContractExecutionStatusFromFailedVMStatus(vmStatus string, forwarder aptos_sdk.AccountAddress) *aptoscap.ReceiverContractExecutionStatus {
	lower := strings.ToLower(vmStatus)
	idx := strings.Index(lower, moveAbortInPrefix)
	if idx < 0 {
		return nil
	}
	rest := strings.TrimSpace(vmStatus[idx+len(moveAbortInPrefix):])
	parts := strings.Split(rest, "::")
	if len(parts) < 2 {
		return nil
	}
	abortModule, err := aptos_sdk.ConvertToAddress(strings.TrimSpace(parts[0]))
	if err != nil {
		return nil
	}
	if *abortModule == forwarder {
		return nil
	}
	rev := aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
	return &rev
}

func getTransmissionID(workflowExecutionID string, request *aptoscap.WriteReportRequest) (TransmissionID, error) {
	rawExecutionID, reportID, err := capcommon.ParseTransmissionComponents(workflowExecutionID, request.Report.RawReport)
	if err != nil {
		return TransmissionID{}, err
	}

	if len(request.Receiver) != 32 {
		return TransmissionID{}, fmt.Errorf("%s receiver address must be 32 bytes, got %d", capcommon.UserError, len(request.Receiver))
	}

	return TransmissionID{
		Receiver:            [32]byte(request.Receiver),
		WorkflowExecutionID: rawExecutionID,
		ReportID:            reportID,
	}, nil
}

func (s *Aptos) validateWriteReportInputs(requestMetadata capabilities.RequestMetadata, request *aptoscap.WriteReportRequest) error {
	if request == nil {
		return fmt.Errorf("nil WriteReportRequest")
	}
	if request.Report == nil {
		return fmt.Errorf("nil Report in WriteReportRequest")
	}
	if len(request.Report.Sigs) == 0 {
		return fmt.Errorf("no signatures provided")
	}

	// TODO: PLEX-3107 move validation to common
	reportMetadata, err := capcommon.DecodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return fmt.Errorf("failed to decode report metadata: %w", err)
	}
	if reportMetadata.Version != 1 {
		return fmt.Errorf("unsupported report version: %d", reportMetadata.Version)
	}

	if reportMetadata.ExecutionID != requestMetadata.WorkflowExecutionID {
		return fmt.Errorf("workflowExecutionID mismatch: report=%s, request=%s",
			reportMetadata.ExecutionID, requestMetadata.WorkflowExecutionID)
	}

	if !strings.EqualFold(reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner) {
		return fmt.Errorf("workflowOwner mismatch: report=%s, request=%s",
			reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner)
	}

	if reportMetadata.WorkflowID != requestMetadata.WorkflowID {
		return fmt.Errorf("workflowID mismatch: report=%s, request=%s",
			reportMetadata.WorkflowID, requestMetadata.WorkflowID)
	}

	//	workflowNames are padded to 10 bytes (20 hex chars)
	reqName := requestMetadata.WorkflowName
	if len(reqName) < 20 {
		reqName += strings.Repeat("0", 20-len(reqName))
	}
	if reportMetadata.WorkflowName != reqName {
		return fmt.Errorf("workflowName in the report does not match WorkflowName in the request metadata. Report WorkflowName: %s, request WorkflowName: %s", reportMetadata.WorkflowName, reqName)
	}

	return nil
}

func (s *Aptos) isUserError(err error) bool {
	return strings.HasPrefix(err.Error(), capcommon.UserError)
}

// pollTransmissionInfo returns the final state of the transmission at this point of the transmission schedule,
// taking into account previous nodes in the queue.
func (wr *writeReport) pollTransmissionInfo(
	ctx context.Context,
	transmissionID TransmissionID,
	queuePosition int,
	telemetryContext monitoring.TelemetryContext,
) (lastValidInfo TransmissionInfo, err error) {
	wr.lggr.Debugw("PollTransmissionInfo called",
		"transmissionID", transmissionID.GetDebugID(),
		"queuePosition", queuePosition,
		"deltaStage", wr.transmissionScheduler.DeltaStage,
	)

	if queuePosition <= 0 {
		wr.lggr.Debugw("Position 0, doing quick retry poll")
		transmissionInfo, err := capcommon.WithQuickRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
			return wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID, nil)
		})
		if err != nil {
			wr.lggr.Errorw("Quick retry poll failed", "error", err)
			return TransmissionInfo{}, err
		}
		wr.lggr.Debugw("Quick retry poll result", "success", transmissionInfo.Success)
		return transmissionInfo, nil
	}

	delay := time.Duration(queuePosition) * wr.transmissionScheduler.DeltaStage
	wr.lggr.Debugw("Polling until slot or state change", "delay", delay, "deltaStage", wr.transmissionScheduler.DeltaStage)

	attempt := 0
	stageTimer := time.NewTimer(delay)
	deltaStagePassed := false
	hadSuccessfulPoll := false
	defer func() {
		stageTimer.Stop()
		if !deltaStagePassed && hadSuccessfulPoll {
			monitoring.LogAndEmitSuccess(ctx, "Transmission found before delta stage has passed",
				wr.lggr, wr.beholderProcessor,
				wr.messageBuilder.BuildWriteReportSuccessfulEarlyReturn(telemetryContext))
		}
	}()

	for {
		if info, infoErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID, nil); infoErr != nil {
			wr.lggr.Debugw("GetTransmissionInfo failed during polling", "error", infoErr, "attempt", attempt)
		} else {
			hadSuccessfulPoll = true
			lastValidInfo = info
			if lastValidInfo.Success {
				wr.lggr.Debugw("Found successful transmission during polling", "attempt", attempt, "transmitter", lastValidInfo.Transmitter.String())
				return lastValidInfo, nil
			}
		}

		wait := (100 * time.Millisecond) << min(attempt, 5) // exponential backoff: 100ms, 200ms, 400ms, 800ms, 1600ms, then capped at 2s
		if wait > 2*time.Second {
			wait = 2 * time.Second
		}
		attempt++

		select {
		case <-ctx.Done():
			hadSuccessfulPoll = false
			wr.lggr.Errorw("Timed out waiting for transmission info", "attempts", attempt)
			return TransmissionInfo{}, fmt.Errorf("timed out waiting for transmission info")
		case <-stageTimer.C:
			deltaStagePassed = true
			if !lastValidInfo.Success {
				if finalInfo, finalErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID, nil); finalErr == nil {
					hadSuccessfulPoll = true
					lastValidInfo = finalInfo
				} else {
					wr.lggr.Debugw("Final GetTransmissionInfo at stage boundary failed", "error", finalErr)
				}
			}
			if !hadSuccessfulPoll {
				wr.lggr.Errorw("All GetTransmissionInfo polls failed during delta stage window, cannot determine transmission state")
				return TransmissionInfo{}, fmt.Errorf("all GetTransmissionInfo polls failed during delta stage window")
			}
			wr.lggr.Debugw("Delta stage has passed, returning transmission info", "success", lastValidInfo.Success, "attempts", attempt)
			return lastValidInfo, nil
		case <-time.After(wait):
		}
	}
}

// logIfSuccessfulTransmitterNotInOrderedList logs when transmission info reports success
// but the transmitter is not in the ordered peer list from p2pConfig.
func (wr *writeReport) logIfSuccessfulTransmitterNotInOrderedList(ctx context.Context, telemetryContext monitoring.TelemetryContext, info TransmissionInfo, orderedTransmitters []string) {
	if !info.Success {
		return
	}
	transmitterAddr := info.Transmitter.StringLong()
	if !slices.Contains(orderedTransmitters, transmitterAddr) {
		wr.lggr.Errorw("successful transmitter not found in orderedTransmitters, p2pConfig may be incomplete or an external entity submitted the report",
			"transmitter", transmitterAddr, "orderedTransmitters", orderedTransmitters)
		monitoring.EmitInitiated(ctx, wr.lggr, wr.beholderProcessor,
			wr.messageBuilder.BuildWriteReportTransmitterMismatch(telemetryContext, transmitterAddr, orderedTransmitters))
	}
}

// GetOrderedTransmitters returns transmitter addresses in queue order (position 0 first)
// for the given transmissionID. PeerIDs are resolved to transmitter addresses via p2pConfig.
// Peers not found in p2pConfig get an empty string to preserve positional ordering.
func (wr *writeReport) getOrderedTransmitters(transmissionID string, p2pConfig map[string]string) []string {
	permuted := wr.transmissionScheduler.GetPermutedOrder(transmissionID)

	var transmitters []string
	for i, peerID := range permuted {
		peerHex := fmt.Sprintf("%x", peerID[:])
		if addr, ok := p2pConfig[peerHex]; ok {
			transmitters = append(transmitters, addr)
		} else {
			wr.lggr.Errorf("GetOrderedTransmitters peerID[%d]=%s not found in p2pConfig, p2pConfig may be incomplete", i, peerHex)
			transmitters = append(transmitters, "")
		}
	}
	return transmitters
}
