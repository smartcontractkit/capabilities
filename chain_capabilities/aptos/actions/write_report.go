package actions

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/metering"
)

func withQuickRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error)) (T, error) {
	return capcommon.WithQuickRetry(ctx, lggr, fn)
}

func withPollingRetry[T any](ctx context.Context, lggr logger.Logger, fn func(context.Context) (T, error)) (T, error) {
	return capcommon.WithPollingRetry(ctx, lggr, fn)
}

// WriteReport validates and submits a signed report to the Aptos chain via the CRE forwarder.
func (s *Aptos) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	s.lggr.Debugw("WriteReport called",
		"workflowExecutionID", metadata.WorkflowExecutionID,
		"workflowID", metadata.WorkflowID,
		"workflowOwner", metadata.WorkflowOwner,
		"hasInput", input != nil,
	)

	// 1. Validate inputs
	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		s.lggr.Errorw("validateWriteReportInputs failed", "error", err)
		return nil, capcommon.NewUserError(err)
	}
	s.lggr.Debugw("inputs validated successfully")

	// 2. Build and submit the transaction via AptosService
	reply, responseMetadata, err := s.executeWriteReport(ctx, input, metadata)
	if err != nil {
		s.lggr.Errorw("executeWriteReport failed", "error", err)
		return nil, capcommon.GetError(err, s.isUserError(err))
	}

	s.lggr.Debugw("WriteReport completed successfully",
		"txStatus", reply.TxStatus,
		"hasTxHash", reply.TxHash != nil,
	)

	return &capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply]{
		Response:         reply,
		ResponseMetadata: responseMetadata,
	}, nil
}

type writeReport struct {
	forwarderClient       CREForwarderClient
	forwarderAddress      aptos_sdk.AccountAddress
	aptosService          types.AptosService
	lggr                  logger.SugaredLogger
	p2pConfig             map[string]string
	chainSelector         uint64
	maxGasAmountLimit     limits.BoundLimiter[uint64]
	reportSizeLimit       limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler transmission_schedule.TransmissionScheduler
}

func (s *Aptos) executeWriteReport(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
) (*aptoscap.WriteReportReply, capabilities.ResponseMetadata, error) {
	wr := &writeReport{
		forwarderClient:       s.forwarderClient,
		forwarderAddress:      s.forwarderAddress,
		aptosService:          s.AptosService,
		lggr:                  s.lggr,
		p2pConfig:             s.p2pConfig,
		chainSelector:         s.chainSelector,
		maxGasAmountLimit:     s.maxGasAmountLimit,
		reportSizeLimit:       s.reportSizeLimit,
		transmissionScheduler: s.transmissionScheduler,
	}
	return wr.execute(ctx, request, metadata)
}

// TODO: handle gas limit bumping if required (PLEX-2580)
// TODO: handle metrics (PLEX-2546)
func (wr *writeReport) execute(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
) (*aptoscap.WriteReportReply, capabilities.ResponseMetadata, error) {
	wr.lggr.Debugw("execute started",
		"workflowExecutionID", metadata.WorkflowExecutionID,
		"hasGasConfig", request.GasConfig != nil,
		"reportLen", len(request.Report.RawReport),
		"numSigs", len(request.Report.Sigs),
		"receiver", hex.EncodeToString(request.Receiver[:]),
	)
	// this helps the node query only relevant transactions when trying to find the txHash, anything before (requestStartTime - 1min) is not relevant
	// the 1min here can be adjusted based on timeout configs and metrics
	requestStartTime := time.Now()

	// Set gas limits: use defaults if not provided, otherwise check against configured limit
	if request.GasConfig == nil {
		request.GasConfig = &aptoscap.GasConfig{}
		limit, limErr := wr.maxGasAmountLimit.Limit(ctx)
		if limErr != nil {
			wr.lggr.Errorw("failed to get gas limit", "error", limErr)
			return nil, capabilities.ResponseMetadata{}, limErr
		}
		request.GasConfig.MaxGasAmount = limit
		wr.lggr.Debugw("using default gas limit", "maxGasAmount", limit)
	} else {
		err := wr.maxGasAmountLimit.Check(ctx, request.GasConfig.MaxGasAmount)
		if err != nil {
			wr.lggr.Errorw("gas config exceeds limit", "maxGasAmount", request.GasConfig.MaxGasAmount, "error", err)
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s provided gas config exceeds limit (maxGasAmount=%d): %w", capcommon.UserError, request.GasConfig.MaxGasAmount, err)
		}
		wr.lggr.Debugw("using provided gas config", "maxGasAmount", request.GasConfig.MaxGasAmount)
	}

	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		wr.lggr.Errorw("getTransmissionID failed", "error", err)
		return &aptoscap.WriteReportReply{}, capabilities.ResponseMetadata{}, err
	}
	wr.lggr.Debugw("transmissionID created", "transmissionID", transmissionID.GetDebugID())

	txHashRetriever := NewTxHashRetriever(wr.forwarderClient, wr.lggr, transmissionID, wr.forwarderAddress.String(), requestStartTime)

	queuePosition := wr.transmissionScheduler.GetQueuePosition(transmissionID.GetDebugID())
	orderedTransmitters := wr.getOrderedTransmitters(transmissionID.GetDebugID(), wr.p2pConfig)
	wr.lggr.Debugw("got queue position",
		"queuePosition", queuePosition,
		"orderedTransmitters", orderedTransmitters,
		"schedule", wr.transmissionScheduler.Schedule,
	)
	// polling here is done based on queue position and deltaStage for one-at-a-time,
	// and as a quick state probe for all-at-once.
	transmissionInfo, err := wr.pollTransmissionInfo(ctx, transmissionID, queuePosition)
	if err != nil {
		wr.lggr.Errorw("pollTransmissionInfo failed", "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get transmission info: %w", err)
	}
	wr.lggr.Debugw("initial pollTransmissionInfo result", "success", transmissionInfo.Success, "transmitter", transmissionInfo.Transmitter.String())

	if transmissionInfo.Success {
		transmitterAddr := transmissionInfo.Transmitter.StringLong()
		if !slices.Contains(orderedTransmitters, transmitterAddr) {
			// TODO: PLEX-2547 emit metric - transmitter not in orderedTransmitters
			wr.lggr.Errorw("successful transmitter not found in orderedTransmitters, p2pConfig may be incomplete or an external entity submitted the report",
				"transmitter", transmitterAddr, "orderedTransmitters", orderedTransmitters)
		}
		wr.lggr.Debugw("report already onchain, retrieving txHash")
		txInfo, txHashErr := txHashRetriever.GetSuccessfulTransmissionInfo(ctx, transmissionInfo.Transmitter)
		if txHashErr != nil {
			wr.lggr.Errorw("report already onchain but failed to retrieve its txHash", "error", txHashErr)
			return nil, capabilities.ResponseMetadata{}, txHashErr
		}
		wr.lggr.Debugw("returning early - report already onchain", "txHash", txInfo.TxHash)
		return wr.buildWriteReportResponse(ctx, aptoscap.TxStatus_TX_STATUS_SUCCESS, txInfo)
	}

	err = wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		wr.lggr.Errorw("report size exceeds limit", "reportSize", len(request.Report.RawReport), "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s report size exceeds limit: %w", capcommon.UserError, err)
	}

	wr.lggr.Debugw("submitting WriteReport transaction",
		"executionID", metadata.WorkflowExecutionID,
		"receiver", hex.EncodeToString(request.Receiver[:]),
		"maxGasAmount", request.GasConfig.MaxGasAmount,
	)

	txReply, err := wr.forwarderClient.InvokeOnReport(ctx, request.Receiver, request.Report, request.GasConfig)
	if err != nil {
		wr.lggr.Errorw("InvokeOnReport failed", "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to invoke forwarder report: %w", err)
	}
	wr.lggr.Debugw("InvokeOnReport returned", "txHash", txReply.TxHash, "txStatus", txReply.TxStatus)

	// polling here is done immediately after submission
	newTransmissionInfo, err := withPollingRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readTransmissionInfo, readTransmissionErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if readTransmissionErr != nil {
			return TransmissionInfo{}, readTransmissionErr
		}
		return readTransmissionInfo, nil
	})

	if err != nil {
		wr.lggr.Errorw("post-submission polling failed", "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed getting transmission info after node submitted the report on chain, %w", err)
	}

	wr.lggr.Debugw("post-submission transmission status", "success", newTransmissionInfo.Success, "transmitter", newTransmissionInfo.Transmitter.String())

	switch newTransmissionInfo.Success {
	case true:
		transmitterAddr := newTransmissionInfo.Transmitter.String()
		if !slices.Contains(orderedTransmitters, transmitterAddr) {
			// TODO: PLEX-2547 emit metric - transmitter not in orderedTransmitters
			wr.lggr.Errorw("successful transmitter not found in orderedTransmitters, p2pConfig may be incomplete or an external entity submitted the report",
				"transmitter", transmitterAddr, "orderedTransmitters", orderedTransmitters)
		}
		txInfo := txInfoFromSubmitReply(txReply)
		if txReply.TxStatus == aptostypes.TxFatal || txReply.TxStatus == aptostypes.TxReverted {
			// Report for this transaction has already been submitted and we sent a duplicate tx onchain, that is why this tx reverted but transmission info still shows success.
			wr.lggr.Debugw("our tx reverted but report is onchain (duplicate), retrieving success hash",
				"ownTxStatus", txReply.TxStatus, "ownTxHash", txReply.TxHash)
			successTxInfo, txHashErr := txHashRetriever.GetSuccessfulTransmissionInfo(ctx, newTransmissionInfo.Transmitter)
			if txHashErr != nil {
				wr.lggr.Errorw("failed to get successful transmission hash after duplicate", "error", txHashErr)
				return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get successful transmission hash: %w", txHashErr)
			}
			txInfo = successTxInfo
		}
		wr.lggr.Debugw("returning SUCCESS", "txHash", txInfo.TxHash)
		return wr.buildWriteReportResponse(ctx, aptoscap.TxStatus_TX_STATUS_SUCCESS, txInfo)
	case false:
		if txReply.TxStatus == aptostypes.TxSuccess {
			wr.lggr.Errorw("unexpected state - local tx succeeded but transmission info shows no success",
				"transmissionID", transmissionID.GetDebugID())
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("unexpected state: local transaction succeeded but transmission info shows no success for %s", transmissionID.GetDebugID())
		}
		ownTxInfo := txInfoFromSubmitReply(txReply)
		wr.lggr.Debugw("transmission failed, searching for tx hashes",
			"ownTxHash", ownTxInfo.TxHash,
			"ownTxStatus", txReply.TxStatus,
			"queuePosition", queuePosition,
		)

		if queuePosition <= 0 {
			wr.lggr.Debugw("position 0 in one-at-a-time mode, returning own failed hash", "txHash", ownTxInfo.TxHash)
			return wr.buildWriteReportResponse(ctx, aptoscap.TxStatus_TX_STATUS_FATAL, ownTxInfo)
		}

		wr.lggr.Debugw("searching prior transmitters for failed tx",
			"queuePosition", queuePosition,
			"orderedTransmittersCount", len(orderedTransmitters),
			"transmissionDebugID", transmissionID.GetDebugID(),
		)
		for i := 0; i < min(queuePosition, len(orderedTransmitters)); i++ {
			if orderedTransmitters[i] == "" {
				// TODO: PLEX-2598 emit metric - p2pConfig incomplete, missing transmitter at this position
				wr.lggr.Warnw("skipping empty transmitter address, p2pConfig is incomplete", "index", i)
				continue
			}
			wr.lggr.Debugw("checking transmitter for failed tx", "index", i, "address", orderedTransmitters[i])
			addr, err := aptos_sdk.ConvertToAddress(orderedTransmitters[i])
			if err != nil {
				wr.lggr.Errorw("failed to convert transmitter address to address", "address", orderedTransmitters[i], "error", err)
				continue
			}
			failedTxInfo, searchErr := txHashRetriever.GetFailedTransmissionInfo(ctx, *addr)
			if searchErr != nil {
				wr.lggr.Debugw("no matching failed tx for prior transmitter", "transmitter", orderedTransmitters[i], "position", i, "err", searchErr)
				continue
			}
			wr.lggr.Debugw("found failed transmission from prior node", "transmitter", orderedTransmitters[i], "position", i, "txHash", failedTxInfo.TxHash)
			return wr.buildWriteReportResponse(ctx, aptoscap.TxStatus_TX_STATUS_FATAL, failedTxInfo)
		}

		// No matching failed tx from prior nodes; return our own hash.
		wr.lggr.Debugw("no prior failed tx found, returning own hash", "txHash", ownTxInfo.TxHash)
		return wr.buildWriteReportResponse(ctx, aptoscap.TxStatus_TX_STATUS_FATAL, ownTxInfo)
	}
	return nil, capabilities.ResponseMetadata{}, nil // should never happen
}

func txInfoFromSubmitReply(txReply *aptostypes.SubmitTransactionReply) txInfo {
	return txInfo{TxHash: txReply.TxHash}
}

func (wr *writeReport) buildWriteReportResponse(
	ctx context.Context,
	status aptoscap.TxStatus,
	info txInfo,
) (*aptoscap.WriteReportReply, capabilities.ResponseMetadata, error) {
	if status != aptoscap.TxStatus_TX_STATUS_SUCCESS {
		resolvedInfo, resolveErr := wr.resolveTxInfo(ctx, info)
		if resolveErr != nil {
			wr.lggr.Warnw("failed to enrich tx info for write report response", "txHash", info.TxHash, "error", resolveErr)
		} else {
			info = resolvedInfo
		}
	}

	classification := aptostypes.ClassifyWriteVmStatus(info.VMStatus)
	reply := &aptoscap.WriteReportReply{TxStatus: status}
	if info.TxHash != "" {
		txHash := info.TxHash
		reply.TxHash = &txHash
	}
	applyFailureClassification(reply, classification)

	feeOctas, err := wr.getTransactionFeeOctas(ctx, info)
	if err != nil {
		wr.lggr.Warnw("failed to resolve transaction fee", "txHash", info.TxHash, "error", err)
		return reply, capabilities.ResponseMetadata{}, nil
	}
	if feeOctas == nil {
		return reply, capabilities.ResponseMetadata{}, nil
	}

	reply.TransactionFee = feeOctas
	return reply, metering.GetResponseMetadataWriteReport(octasToAPT(*feeOctas), wr.chainSelector), nil
}

func (wr *writeReport) resolveTxInfo(ctx context.Context, info txInfo) (txInfo, error) {
	if info.TxHash == "" {
		return info, nil
	}
	if info.GasUsed != 0 && info.GasUnitPrice != 0 && info.VMStatus != "" {
		return info, nil
	}

	reply, err := wr.aptosService.TransactionByHash(ctx, aptostypes.TransactionByHashRequest{Hash: info.TxHash})
	if err != nil {
		return info, fmt.Errorf("failed to get transaction by hash: %w", err)
	}
	if reply == nil || reply.Transaction == nil {
		return info, fmt.Errorf("nil transaction by hash reply for %s", info.TxHash)
	}

	var txData userTxData
	if err := json.Unmarshal(reply.Transaction.Data, &txData); err != nil {
		return info, fmt.Errorf("failed to unmarshal transaction data: %w", err)
	}

	if info.GasUsed == 0 {
		info.GasUsed = txData.GasUsed
	}
	if info.GasUnitPrice == 0 {
		info.GasUnitPrice = txData.GasUnitPrice
	}
	if info.VMStatus == "" {
		info.VMStatus = txData.VMStatus
	}

	return info, nil
}

func (wr *writeReport) getTransactionFeeOctas(ctx context.Context, info txInfo) (*uint64, error) {
	if info.GasUsed != 0 || info.GasUnitPrice != 0 {
		return calculateTransactionFeeOctas(info.GasUsed, info.GasUnitPrice)
	}
	if info.TxHash == "" {
		return nil, nil
	}

	reply, err := wr.aptosService.TransactionByHash(ctx, aptostypes.TransactionByHashRequest{Hash: info.TxHash})
	if err != nil {
		return nil, fmt.Errorf("failed to get transaction by hash: %w", err)
	}
	if reply == nil || reply.Transaction == nil {
		return nil, fmt.Errorf("nil transaction by hash reply for %s", info.TxHash)
	}

	var txData userTxData
	if err := json.Unmarshal(reply.Transaction.Data, &txData); err != nil {
		return nil, fmt.Errorf("failed to unmarshal transaction data: %w", err)
	}
	return calculateTransactionFeeOctas(txData.GasUsed, txData.GasUnitPrice)
}

func calculateTransactionFeeOctas(gasUsed, gasUnitPrice uint64) (*uint64, error) {
	fee := new(big.Int).SetUint64(gasUsed)
	fee.Mul(fee, new(big.Int).SetUint64(gasUnitPrice))
	if !fee.IsUint64() {
		return nil, fmt.Errorf("transaction fee exceeds uint64 range")
	}
	feeOctas := fee.Uint64()
	return &feeOctas, nil
}

func applyFailureClassification(reply *aptoscap.WriteReportReply, classification aptostypes.WriteFailureClassification) {
	if reply.TxStatus == aptoscap.TxStatus_TX_STATUS_SUCCESS {
		return
	}

	if classification.ReceiverExecutionStatus == aptostypes.ReceiverExecutionStatusReverted {
		status := aptoscap.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED
		reply.ReceiverContractExecutionStatus = &status
	}

	if msg := classification.MessagePtr(); msg != nil {
		reply.ErrorMessage = msg
	}
}

func octasToAPT(feeOctas uint64) *big.Float {
	return new(big.Float).Quo(new(big.Float).SetUint64(feeOctas), big.NewFloat(1e8))
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

	return nil
}

func (s *Aptos) isUserError(err error) bool {
	return strings.HasPrefix(err.Error(), capcommon.UserError)
}

// pollTransmissionInfo returns the final state of the transmission at this point of the transmission schedule,
// taking into account previous nodes in the queue.
// TODO: copied from evm, can be reused
func (wr *writeReport) pollTransmissionInfo(
	ctx context.Context,
	transmissionID TransmissionID,
	queuePosition int,
) (lastValidInfo TransmissionInfo, err error) {
	wr.lggr.Debugw("pollTransmissionInfo called",
		"transmissionID", transmissionID.GetDebugID(),
		"queuePosition", queuePosition,
		"deltaStage", wr.transmissionScheduler.DeltaStage,
	)

	if queuePosition <= 0 {
		wr.lggr.Debugw("doing quick retry poll")
		transmissionInfo, err := withQuickRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
			return wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		})
		if err != nil {
			wr.lggr.Errorw("quick retry poll failed", "error", err)
			return TransmissionInfo{}, err
		}
		wr.lggr.Debugw("quick retry poll result", "success", transmissionInfo.Success)
		return transmissionInfo, nil
	}

	delay := time.Duration(queuePosition) * wr.transmissionScheduler.DeltaStage
	wr.lggr.Debugw("polling until slot or state change", "delay", delay, "deltaStage", wr.transmissionScheduler.DeltaStage)

	attempt := 0
	stageTimer := time.NewTimer(delay)
	stageTimerFired := false
	defer func() {
		stageTimer.Stop()
		if !stageTimerFired {
			wr.lggr.Debugw("transmission found before delta stage has passed")
		}
	}()

	for {
		if info, infoErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID); infoErr != nil {
			wr.lggr.Debugw("GetTransmissionInfo failed during polling", "error", infoErr, "attempt", attempt)
		} else {
			lastValidInfo = info
			if lastValidInfo.Success {
				wr.lggr.Debugw("found successful transmission during polling", "attempt", attempt, "transmitter", lastValidInfo.Transmitter.String())
				return lastValidInfo, nil
			}
		}

		wait := (100 * time.Millisecond) << min(attempt, 5) // up to 3.2s wait
		if wait > 2*time.Second {
			wait = 2 * time.Second
		}
		attempt++

		select {
		case <-ctx.Done():
			wr.lggr.Errorw("timed out waiting for transmission info", "attempts", attempt)
			return TransmissionInfo{}, fmt.Errorf("timed out waiting for transmission info")
		case <-stageTimer.C:
			stageTimerFired = true
			wr.lggr.Debugw("delta stage has passed, returning transmission info", "success", lastValidInfo.Success, "attempts", attempt)
			return lastValidInfo, nil
		case <-time.After(wait):
		}
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
			wr.lggr.Errorf("getOrderedTransmitters peerID[%d]=%s not found in p2pConfig, p2pConfig may be incomplete", i, peerHex)
			transmitters = append(transmitters, "")
		}
	}
	return transmitters
}
