package actions

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"

	"github.com/jpillora/backoff"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/retry"
)

const userError = "user error:"

// TODO PLEX-1920 carry out shared helpers
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

// WriteReport validates and submits a signed report to the Aptos chain via the CRE forwarder.
// It handles only the simple successful case for now.
func (s *Aptos) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	// 1. Validate inputs
	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		return nil, NewUserError(err)
	}

	// 2. Build and submit the transaction via AptosService
	reply, err := s.executeWriteReport(ctx, input, metadata)
	if err != nil {
		return nil, GetError(err, s.isUserError(err))
	}

	return &capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply]{
		Response:         reply,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}, nil
}

type writeReport struct {
	forwarderClient       CREForwarderClient
	forwarderAddress      aptos_sdk.AccountAddress
	lggr                  logger.SugaredLogger
	p2pConfig             map[string]string
	maxGasAmountLimit     limits.BoundLimiter[uint64]
	reportSizeLimit       limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler TransmissionScheduler
}

func (s *Aptos) executeWriteReport(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
) (*aptoscap.WriteReportReply, error) {
	wr := &writeReport{
		forwarderClient:       s.forwarderClient,
		forwarderAddress:      s.forwarderAddress,
		lggr:                  s.lggr,
		p2pConfig:             s.p2pConfig,
		maxGasAmountLimit:     s.maxGasAmountLimit,
		reportSizeLimit:       s.reportSizeLimit,
		transmissionScheduler: s.transmissionScheduler,
	}
	return wr.execute(ctx, request, metadata)
}

// TODO: handle billing fees PLEX-2578
// TODO: handle gas bumping if required PLEX-2580
// TODO: handle metrics PLEX-2546
// TODO: query failed transaction using transaction by hash from aptos service and get vmstatus
func (wr *writeReport) execute(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
) (*aptoscap.WriteReportReply, error) {
	// this helps the node query only relevant transactions when trying to find the txHash, anything before (requestStartTime - 1min) is not relevant
	// the 1min here can be adjusted based on timeout configs and metrics
	requestStartTime := time.Now()

	// Set gas limits: use defaults if not provided, otherwise check against configured limit
	if request.GasConfig == nil {
		request.GasConfig = &aptoscap.GasConfig{}
		limit, limErr := wr.maxGasAmountLimit.Limit(ctx)
		if limErr != nil {
			return nil, limErr
		}
		request.GasConfig.MaxGasAmount = limit
	} else {
		err := wr.maxGasAmountLimit.Check(ctx, request.GasConfig.MaxGasAmount)
		if err != nil {
			return nil, fmt.Errorf("%s provided gas config exceeds limit (maxGasAmount=%d): %w", userError, request.GasConfig.MaxGasAmount, err)
		}
	}

	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return &aptoscap.WriteReportReply{}, err
	}

	txHashRetriever := NewTxHashRetriever(wr.forwarderClient, wr.lggr, transmissionID, wr.forwarderAddress.String(), requestStartTime)

	queuePosition := wr.transmissionScheduler.GetQueuePosition(transmissionID.GetDebugID())
	// polling here is done based on queue position and deltaStage
	transmissionInfo, err := wr.pollTransmissionInfo(ctx, transmissionID, queuePosition)
	if err != nil {
		return nil, fmt.Errorf("failed to get transmission info: %w", err)
	}

	if transmissionInfo.Success {
		txHash, txHashErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx, transmissionInfo.Transmitter)
		if txHashErr != nil {
			wr.lggr.Errorw("Report already onchain but failed to retrieve its txHash", "error", txHashErr)
			return nil, txHashErr
		}
		wr.lggr.Infow("Returning without a transmission attempt - report already onchain", "txHash", txHash)
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   &txHash,
		}, nil
	}
	// TODO: we can exit here if we find F+1 failed transactions, but thats expensive time and i/o wise.
	// emit metrics here to understand if its worth investing time here.
	// maybe do a lucky poll of node0's failed tx and see if we get lucky

	err = wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		return nil, fmt.Errorf("%s report size exceeds limit: %w", userError, err)
	}

	wr.lggr.Debugw("Submitting WriteReport transaction", "executionID", metadata.WorkflowExecutionID, "receiver", hex.EncodeToString(request.Receiver[:]))

	txReply, err := wr.forwarderClient.InvokeOnReport(ctx, request.Receiver, request.Report, request.GasConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke forwarder report: %w", err)
	}

	// polling here is done immediately after submission
	newTransmissionInfo, err := withPollingRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readTransmissionInfo, readTransmissionErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if readTransmissionErr != nil {
			return TransmissionInfo{}, readTransmissionErr
		}
		return readTransmissionInfo, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed getting transmission info after node submitted the report on chain, %w", err)
	}

	wr.lggr.Infow("Got final transmission status", "success", newTransmissionInfo.Success)

	switch newTransmissionInfo.Success {
	case true:
		txHash := txReply.TxHash
		if txReply.TxStatus == aptostypes.TxFatal || txReply.TxStatus == aptostypes.TxReverted {
			// Report for this transaction has already been submitted and we sent a duplicate tx onchain, that is why this tx reverted but transmission info still shows success.
			successHash, txHashErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx, newTransmissionInfo.Transmitter)
			if txHashErr != nil {
				return nil, fmt.Errorf("failed to get successful transmission hash: %w", txHashErr)
			}
			txHash = successHash
		}
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   &txHash,
		}, nil
	case false:
		if txReply.TxStatus == aptostypes.TxSuccess {
			return nil, fmt.Errorf("unexpected state: local transaction succeeded but transmission info shows no success for %s", transmissionID.GetDebugID())
		}
		ownTxHash := txReply.TxHash

		// Position 0 node has no prior nodes to check; return its own failed tx hash.
		if queuePosition <= 0 {
			return &aptoscap.WriteReportReply{
				TxStatus: aptoscap.TxStatus_TX_STATUS_FATAL,
				TxHash:   &ownTxHash,
			}, nil
		}

		// Search preceding transmitters (position 0 through position-1) for a matching failed tx.
		orderedTransmitters := wr.transmissionScheduler.GetOrderedTransmitters(transmissionID.GetDebugID())
		for i := 0; i < queuePosition && i < len(orderedTransmitters); i++ {
			var addr aptos_sdk.AccountAddress
			if parseErr := addr.ParseStringRelaxed(orderedTransmitters[i]); parseErr != nil {
				wr.lggr.Warnw("Failed to parse transmitter address, skipping", "address", orderedTransmitters[i], "err", parseErr)
				continue
			}
			failedHash, searchErr := txHashRetriever.GetFailedTransmissionHash(ctx, addr)
			if searchErr != nil {
				wr.lggr.Debugw("No matching failed tx found for prior transmitter", "transmitter", orderedTransmitters[i], "position", i, "err", searchErr)
				continue
			}
			wr.lggr.Infow("Found matching failed transmission from prior node", "transmitter", orderedTransmitters[i], "position", i, "txHash", failedHash)
			return &aptoscap.WriteReportReply{
				TxStatus: aptoscap.TxStatus_TX_STATUS_FATAL,
				TxHash:   &failedHash,
			}, nil
		}

		// No matching failed tx from prior nodes; return our own hash.
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_FATAL,
			TxHash:   &ownTxHash,
		}, nil
	}
	return nil, fmt.Errorf("transmission state not expected after submit")
}

func getTransmissionID(workflowExecutionID string, request *aptoscap.WriteReportRequest) (TransmissionID, error) {
	rawExecutionID, err := hex.DecodeString(workflowExecutionID)
	if err != nil {
		return TransmissionID{}, err
	}

	if len(rawExecutionID) != 32 {
		return TransmissionID{}, fmt.Errorf("workflowExecutionID must be 32 bytes, got %d", len(rawExecutionID))
	}

	reportMetadata, err := decodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return TransmissionID{}, fmt.Errorf("%s failed to decode report metadata: %v", userError, err)
	}

	reportID, err := hex.DecodeString(reportMetadata.ReportID)
	if err != nil {
		return TransmissionID{}, fmt.Errorf("%s failed to decode report ID: %v", userError, err)
	}
	if len(reportID) != 2 {
		return TransmissionID{}, fmt.Errorf("%s report ID is of wrong length: %d bytes, expected 2 bytes", userError, len(reportID))
	}

	if len(request.Receiver) != 32 {
		return TransmissionID{}, fmt.Errorf("%s receiver address must be 32 bytes, got %d", userError, len(request.Receiver))
	}

	transmissionID := TransmissionID{
		Receiver:            [32]byte(request.Receiver),
		WorkflowExecutionID: [32]byte(rawExecutionID),
		ReportID:            [2]byte(reportID),
	}
	return transmissionID, nil
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

	reportMetadata, err := decodeReportMetadata(request.Report.RawReport)
	if err != nil {
		return err
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

func decodeReportMetadata(data []byte) (ocrtypes.Metadata, error) {
	metadata, _, err := ocrtypes.Decode(data)
	return metadata, err
}

func (s *Aptos) isUserError(err error) bool {
	return strings.HasPrefix(err.Error(), "user error:")
}

// pollTransmissionInfo returns the final state of the transmission at this point of the transmission schedule,
// taking into account previous nodes in the queue.
func (wr *writeReport) pollTransmissionInfo(
	ctx context.Context,
	transmissionID TransmissionID,
	queuePosition int,
) (lastValidInfo TransmissionInfo, err error) {
	if queuePosition <= 0 {
		transmissionInfo, err := withQuickRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
			return wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		})
		if err != nil {
			return TransmissionInfo{}, err
		}
		return transmissionInfo, nil
	}

	delay := time.Duration(queuePosition) * wr.transmissionScheduler.deltaStage
	wr.lggr.Infow("Polling until slot or state change", "delay", delay, "deltaStage", wr.transmissionScheduler.deltaStage)

	attempt := 0
	stageTimer := time.NewTimer(delay)
	stageTimerFired := false
	defer func() {
		stageTimer.Stop()
		if !stageTimerFired {
			wr.lggr.Infow("Transmission found before delta stage has passed")
		}
	}()

	for {
		if info, infoErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID); infoErr != nil {
			wr.lggr.Debugw("GetTransmissionInfo failed during polling", "error", infoErr, "attempt", attempt)
		} else {
			lastValidInfo = info
			if lastValidInfo.Success {
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
			return TransmissionInfo{}, fmt.Errorf("timed out waiting for transmission info")
		case <-stageTimer.C:
			stageTimerFired = true
			wr.lggr.Infow("Delta stage has passed, returning transmission info", "success", lastValidInfo.Success)
			return lastValidInfo, nil
		case <-time.After(wait):
		}
	}
}
