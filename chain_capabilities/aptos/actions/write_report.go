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

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

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
	reply, meteringMetadata, err := s.executeWriteReport(ctx, input, metadata)
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
		ResponseMetadata: meteringMetadata,
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
	transmissionScheduler ts.TransmissionScheduler
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
// TODO: handle ReceiverContractExecutionStatus in WriteReportReply (PLEX-2597)
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
		return nil, capabilities.ResponseMetadata{}, err
	}
	wr.lggr.Debugw("transmissionID created", "transmissionID", transmissionID.GetDebugID())

	txHashRetriever := NewTxHashRetriever(wr.forwarderClient, wr.lggr, transmissionID, wr.forwarderAddress.String(), requestStartTime)

	queuePosition := wr.transmissionScheduler.GetQueuePosition(transmissionID.GetDebugID())
	orderedTransmitters := wr.getOrderedTransmitters(transmissionID.GetDebugID(), wr.p2pConfig)
	wr.lggr.Debugw("got queue position", "queuePosition", queuePosition, "orderedTransmitters", orderedTransmitters)
	// polling here is done based on queue position and deltaStage
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
		txResult, txHashErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx, transmissionInfo.Transmitter)
		if txHashErr != nil {
			wr.lggr.Errorw("report already onchain but failed to retrieve its txHash", "error", txHashErr)
			return nil, capabilities.ResponseMetadata{}, txHashErr
		}
		reply := &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   &txResult.TxHash,
		}
		feeOctas := txResult.GasUsed * txResult.GasUnitPrice
		reply.TransactionFee = &feeOctas
		return reply, capabilities.ResponseMetadata{}, nil
	}
	// TODO: we can exit here if we find F+1 failed transactions, but thats expensive time and i/o wise.
	// emit metrics here to understand if its worth investing time here over writing to a cheap chain and failing.
	// maybe do a poll of node0's failed tx and see if we get lucky, if we do find a matching failed tx, we can use vmstatus to exit early on user errors.

	err = wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		wr.lggr.Errorw("report size exceeds limit", "reportSize", len(request.Report.RawReport), "error", err)
		return nil, capabilities.ResponseMetadata{}, fmt.Errorf("%s report size exceeds limit: %w", capcommon.UserError, err)
	}

	wr.lggr.Debugw("Submitting WriteReport transaction",
		"executionID", metadata.WorkflowExecutionID,
		"receiver", hex.EncodeToString(request.Receiver[:]),
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

	var txFeeOctas *uint64
	var ownVmStatus string
	var meteringMetadata capabilities.ResponseMetadata
	feeInOctas, ownVmStatus, feeErr := wr.getTxnInfoFromChain(ctx, txReply.TxHash)
	if feeErr != nil {
		wr.lggr.Errorw("failed to get transaction fee, using zero for metering", "txHash", txReply.TxHash, "error", feeErr)
		meteringMetadata = metering.GetResponseMetadataWriteReport(big.NewFloat(0), wr.chainSelector)
		// TODO: PLEX-2546 emit metric - failed to get transaction fee
	} else {
		txFeeOctas = &feeInOctas
		feeInAPT := new(big.Float).Quo(new(big.Float).SetUint64(feeInOctas), big.NewFloat(1e8))
		wr.lggr.Debugw("WriteReport fee", "feeInAPT", feeInAPT.String(), "feeInOctas", feeInOctas)
		meteringMetadata = metering.GetResponseMetadataWriteReport(feeInAPT, wr.chainSelector)
	}

	switch newTransmissionInfo.Success {
	case true:
		transmitterAddr := newTransmissionInfo.Transmitter.String()
		if !slices.Contains(orderedTransmitters, transmitterAddr) {
			// TODO: PLEX-2547 emit metric - transmitter not in orderedTransmitters
			wr.lggr.Errorw("successful transmitter not found in orderedTransmitters, p2pConfig may be incomplete or an external entity submitted the report",
				"transmitter", transmitterAddr, "orderedTransmitters", orderedTransmitters)
		}

		if txReply.TxStatus == aptostypes.TxFatal || txReply.TxStatus == aptostypes.TxReverted {
			// Report for this transaction has already been submitted and we sent a duplicate tx onchain, that is why this tx reverted but transmission info still shows success.
			wr.lggr.Debugw("our tx reverted but report is onchain (duplicate), retrieving success hash",
				"ownTxStatus", txReply.TxStatus, "ownTxHash", txReply.TxHash)
			successResult, txHashErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx, newTransmissionInfo.Transmitter)
			if txHashErr != nil {
				wr.lggr.Errorw("failed to get successful transmission hash after duplicate", "error", txHashErr)
				return nil, capabilities.ResponseMetadata{}, fmt.Errorf("failed to get successful transmission hash: %w", txHashErr)
			}
			feeOctas := successResult.GasUsed * successResult.GasUnitPrice
			txFeeOctas = &feeOctas
			return &aptoscap.WriteReportReply{
				TxStatus:       aptoscap.TxStatus_TX_STATUS_SUCCESS,
				TxHash:         &successResult.TxHash,
				TransactionFee: txFeeOctas,
			}, capabilities.ResponseMetadata{}, nil
		}

		return &aptoscap.WriteReportReply{
			TxStatus:       aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:         &txReply.TxHash,
			TransactionFee: txFeeOctas,
		}, meteringMetadata, nil
	case false:
		if txReply.TxStatus == aptostypes.TxSuccess {
			wr.lggr.Errorw("unexpected state - local tx succeeded but transmission info shows no success",
				"transmissionID", transmissionID.GetDebugID())
			return nil, capabilities.ResponseMetadata{}, fmt.Errorf("unexpected state: local transaction succeeded but transmission info shows no success for %s", transmissionID.GetDebugID())
		}
		// Position 0 node has no prior nodes to check; return its own failed tx hash.
		if queuePosition <= 0 {
			wr.lggr.Debugw("position 0, returning own failed hash", "txHash", txReply.TxHash, "vmStatus", ownVmStatus)
			return &aptoscap.WriteReportReply{
				TxStatus:       aptoscap.TxStatus_TX_STATUS_FATAL,
				TxHash:         &txReply.TxHash,
				TransactionFee: txFeeOctas,
				ErrorMessage:   ptrIfNonEmpty(ownVmStatus),
				// TODO: PLEX-2597 populate ReceiverContractExecutionStatus based on vmStatus
			}, meteringMetadata, nil
		}

		// Search preceding transmitters (position 0 through position-1) for a matching failed tx.
		wr.lggr.Debugw("searching preceding transmitters for failed tx",
			"queuePosition", queuePosition,
			"orderedTransmittersCount", len(orderedTransmitters),
			"transmissionDebugID", transmissionID.GetDebugID(),
		)
		for i := 0; i < queuePosition && i < len(orderedTransmitters); i++ {
			if orderedTransmitters[i] == "" {
				// TODO: PLEX-2598 emit metric - p2pConfig incomplete, missing transmitter at this position
				wr.lggr.Warnw("skipping empty transmitter address, p2pConfig is incomplete", "index", i)
				continue
			}
			wr.lggr.Debugw("checking prior transmitter", "index", i, "address", orderedTransmitters[i])
			addr, err := aptos_sdk.ConvertToAddress(orderedTransmitters[i])
			if err != nil {
				wr.lggr.Errorw("failed to convert transmitter address to address", "address", orderedTransmitters[i], "error", err)
				continue
			}
			failedResult, searchErr := txHashRetriever.GetFailedTransmissionHash(ctx, *addr)
			if searchErr != nil {
				wr.lggr.Debugw("no matching failed tx for prior transmitter", "transmitter", orderedTransmitters[i], "position", i, "err", searchErr)
				continue
			}
			wr.lggr.Debugw("found failed transmission from prior node", "transmitter", orderedTransmitters[i], "position", i, "txHash", failedResult.TxHash, "vmStatus", failedResult.VmStatus)
			feeOctas := failedResult.GasUsed * failedResult.GasUnitPrice
			txFeeOctas = &feeOctas
			return &aptoscap.WriteReportReply{
				TxStatus:       aptoscap.TxStatus_TX_STATUS_FATAL,
				TxHash:         &failedResult.TxHash,
				TransactionFee: txFeeOctas,
				ErrorMessage:   ptrIfNonEmpty(failedResult.VmStatus),
				// TODO: PLEX-2597 populate ReceiverContractExecutionStatus based on vmStatus
			}, capabilities.ResponseMetadata{}, nil
		}

		// No matching failed tx from prior nodes; return our own hash.
		wr.lggr.Debugw("no prior failed tx found, returning own hash", "txHash", txReply.TxHash, "vmStatus", ownVmStatus)
		return &aptoscap.WriteReportReply{
			TxStatus:       aptoscap.TxStatus_TX_STATUS_FATAL, // TODO: do we need TX_STATUS_ABORTED at all ?
			TxHash:         &txReply.TxHash,
			TransactionFee: txFeeOctas,
			ErrorMessage:   ptrIfNonEmpty(ownVmStatus),
			// TODO: PLEX-2597 populate ReceiverContractExecutionStatus based on vmStatus
		}, meteringMetadata, nil
	}
	return nil, capabilities.ResponseMetadata{}, nil // should never happen
}

// getTxnInfoFromChain returns the transaction fee in octas (gasUsed * gasUnitPrice) and
// the VM status string by calling AptosService.TransactionByHash and unmarshaling the
// transaction payload (gas fields and VmStatus).
func (wr *writeReport) getTxnInfoFromChain(ctx context.Context, txHash string) (uint64, string, error) {
	reply, err := withQuickRetry(ctx, wr.lggr, func(ctx context.Context) (*aptostypes.TransactionByHashReply, error) {
		return wr.aptosService.TransactionByHash(ctx, aptostypes.TransactionByHashRequest{Hash: txHash})
	})
	if err != nil {
		return 0, "", fmt.Errorf("failed to get transaction by hash: %w", err)
	}
	var txData userTxData
	if err := json.Unmarshal(reply.Transaction.Data, &txData); err != nil {
		return 0, "", fmt.Errorf("failed to unmarshal transaction data: %w", err)
	}

	return txData.GasUsed * txData.GasUnitPrice, txData.VmStatus, nil
}

func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
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
		wr.lggr.Debugw("position 0, doing quick retry poll")
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
