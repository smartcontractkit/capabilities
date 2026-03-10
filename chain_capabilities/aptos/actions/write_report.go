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
func (s *Aptos) WriteReport(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.WriteReportRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.WriteReportReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	s.lggr.Infow("TestingAptosWriteCap: WriteReport called",
		"workflowExecutionID", metadata.WorkflowExecutionID,
		"workflowID", metadata.WorkflowID,
		"workflowOwner", metadata.WorkflowOwner,
		"hasInput", input != nil,
	)

	// 1. Validate inputs
	if err := s.validateWriteReportInputs(metadata, input); err != nil {
		s.lggr.Errorw("TestingAptosWriteCap: validateWriteReportInputs failed", "error", err)
		return nil, NewUserError(err)
	}
	s.lggr.Infow("TestingAptosWriteCap: inputs validated successfully")

	// 2. Build and submit the transaction via AptosService
	reply, err := s.executeWriteReport(ctx, input, metadata)
	if err != nil {
		s.lggr.Errorw("TestingAptosWriteCap: executeWriteReport failed", "error", err)
		return nil, GetError(err, s.isUserError(err))
	}

	s.lggr.Infow("TestingAptosWriteCap: WriteReport completed successfully",
		"txStatus", reply.TxStatus,
		"hasTxHash", reply.TxHash != nil,
	)

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

// TODO: handle billing fees / populate transaction fees in WriteReportReply (PLEX-2578)
// TODO: handle gas limit bumping if required (PLEX-2580)
// TODO: handle metrics (PLEX-2546)
// TODO: populate error message and ReceiverContractExecutionStatus in WriteReportReply by using vmstatus received from failed tx (PLEX-2597)
func (wr *writeReport) execute(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
) (*aptoscap.WriteReportReply, error) {
	wr.lggr.Infow("TestingAptosWriteCap: execute started",
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
			wr.lggr.Errorw("TestingAptosWriteCap: failed to get gas limit", "error", limErr)
			return nil, limErr
		}
		request.GasConfig.MaxGasAmount = limit
		wr.lggr.Infow("TestingAptosWriteCap: using default gas limit", "maxGasAmount", limit)
	} else {
		err := wr.maxGasAmountLimit.Check(ctx, request.GasConfig.MaxGasAmount)
		if err != nil {
			wr.lggr.Errorw("TestingAptosWriteCap: gas config exceeds limit", "maxGasAmount", request.GasConfig.MaxGasAmount, "error", err)
			return nil, fmt.Errorf("%s provided gas config exceeds limit (maxGasAmount=%d): %w", userError, request.GasConfig.MaxGasAmount, err)
		}
		wr.lggr.Infow("TestingAptosWriteCap: using provided gas config", "maxGasAmount", request.GasConfig.MaxGasAmount)
	}

	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		wr.lggr.Errorw("TestingAptosWriteCap: getTransmissionID failed", "error", err)
		return &aptoscap.WriteReportReply{}, err
	}
	wr.lggr.Infow("TestingAptosWriteCap: transmissionID created", "transmissionID", transmissionID.GetDebugID())

	txHashRetriever := NewTxHashRetriever(wr.forwarderClient, wr.lggr, transmissionID, wr.forwarderAddress.String(), requestStartTime)

	queuePosition := wr.transmissionScheduler.GetQueuePosition(transmissionID.GetDebugID())
	wr.lggr.Infow("TestingAptosWriteCap: got queue position", "queuePosition", queuePosition)
	// polling here is done based on queue position and deltaStage
	transmissionInfo, err := wr.pollTransmissionInfo(ctx, transmissionID, queuePosition)
	if err != nil {
		wr.lggr.Errorw("TestingAptosWriteCap: pollTransmissionInfo failed", "error", err)
		return nil, fmt.Errorf("failed to get transmission info: %w", err)
	}
	wr.lggr.Infow("TestingAptosWriteCap: initial pollTransmissionInfo result", "success", transmissionInfo.Success, "transmitter", transmissionInfo.Transmitter.String())

	if transmissionInfo.Success {
		wr.lggr.Infow("TestingAptosWriteCap: report already onchain, retrieving txHash")
		txHash, txHashErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx, transmissionInfo.Transmitter)
		if txHashErr != nil {
			wr.lggr.Errorw("TestingAptosWriteCap: report already onchain but failed to retrieve its txHash", "error", txHashErr)
			return nil, txHashErr
		}
		wr.lggr.Infow("TestingAptosWriteCap: returning early - report already onchain", "txHash", txHash)
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   &txHash,
		}, nil
	}
	// TODO: we can exit here if we find F+1 failed transactions, but thats expensive time and i/o wise.
	// emit metrics here to understand if its worth investing time here.
	// maybe do a poll of node0's failed tx and see if we get lucky

	err = wr.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		wr.lggr.Errorw("TestingAptosWriteCap: report size exceeds limit", "reportSize", len(request.Report.RawReport), "error", err)
		return nil, fmt.Errorf("%s report size exceeds limit: %w", userError, err)
	}

	wr.lggr.Infow("TestingAptosWriteCap: submitting WriteReport transaction",
		"executionID", metadata.WorkflowExecutionID,
		"receiver", hex.EncodeToString(request.Receiver[:]),
		"maxGasAmount", request.GasConfig.MaxGasAmount,
	)

	txReply, err := wr.forwarderClient.InvokeOnReport(ctx, request.Receiver, request.Report, request.GasConfig)
	if err != nil {
		wr.lggr.Errorw("TestingAptosWriteCap: InvokeOnReport failed", "error", err)
		return nil, fmt.Errorf("failed to invoke forwarder report: %w", err)
	}
	wr.lggr.Infow("TestingAptosWriteCap: InvokeOnReport returned", "txHash", txReply.TxHash, "txStatus", txReply.TxStatus)

	// polling here is done immediately after submission
	newTransmissionInfo, err := withPollingRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readTransmissionInfo, readTransmissionErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if readTransmissionErr != nil {
			return TransmissionInfo{}, readTransmissionErr
		}
		return readTransmissionInfo, nil
	})

	if err != nil {
		wr.lggr.Errorw("TestingAptosWriteCap: post-submission polling failed", "error", err)
		return nil, fmt.Errorf("failed getting transmission info after node submitted the report on chain, %w", err)
	}

	wr.lggr.Infow("TestingAptosWriteCap: post-submission transmission status", "success", newTransmissionInfo.Success, "transmitter", newTransmissionInfo.Transmitter.String())

	switch newTransmissionInfo.Success {
	case true:
		txHash := txReply.TxHash
		if txReply.TxStatus == aptostypes.TxFatal || txReply.TxStatus == aptostypes.TxReverted {
			// Report for this transaction has already been submitted and we sent a duplicate tx onchain, that is why this tx reverted but transmission info still shows success.
			wr.lggr.Infow("TestingAptosWriteCap: our tx reverted but report is onchain (duplicate), retrieving success hash",
				"ownTxStatus", txReply.TxStatus, "ownTxHash", txReply.TxHash)
			successHash, txHashErr := txHashRetriever.GetSuccessfulTransmissionHash(ctx, newTransmissionInfo.Transmitter)
			if txHashErr != nil {
				wr.lggr.Errorw("TestingAptosWriteCap: failed to get successful transmission hash after duplicate", "error", txHashErr)
				return nil, fmt.Errorf("failed to get successful transmission hash: %w", txHashErr)
			}
			txHash = successHash
		}
		wr.lggr.Infow("TestingAptosWriteCap: returning SUCCESS", "txHash", txHash)
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   &txHash,
		}, nil
	case false:
		if txReply.TxStatus == aptostypes.TxSuccess {
			wr.lggr.Errorw("TestingAptosWriteCap: unexpected state - local tx succeeded but transmission info shows no success",
				"transmissionID", transmissionID.GetDebugID())
			return nil, fmt.Errorf("unexpected state: local transaction succeeded but transmission info shows no success for %s", transmissionID.GetDebugID())
		}
		ownTxHash := txReply.TxHash
		wr.lggr.Infow("TestingAptosWriteCap: transmission failed, searching for tx hashes",
			"ownTxHash", ownTxHash, "ownTxStatus", txReply.TxStatus, "queuePosition", queuePosition)

		// Position 0 node has no prior nodes to check; return its own failed tx hash.
		if queuePosition <= 0 {
			wr.lggr.Infow("TestingAptosWriteCap: position 0, returning own failed hash", "txHash", ownTxHash)
			return &aptoscap.WriteReportReply{
				TxStatus: aptoscap.TxStatus_TX_STATUS_FATAL,
				TxHash:   &ownTxHash,
			}, nil
		}

		// Search preceding transmitters (position 0 through position-1) for a matching failed tx.
		orderedTransmitters := wr.transmissionScheduler.GetOrderedTransmitters(transmissionID.GetDebugID())
		wr.lggr.Infow("TestingAptosWriteCap: searching preceding transmitters for failed tx",
			"queuePosition", queuePosition,
			"orderedTransmitters", orderedTransmitters,
			"orderedTransmittersCount", len(orderedTransmitters),
			"transmissionDebugID", transmissionID.GetDebugID(),
			"p2pConfig", wr.p2pConfig,
		)
		for i := 0; i < queuePosition && i < len(orderedTransmitters); i++ {
			wr.lggr.Infow("TestingAptosWriteCap: checking prior transmitter", "index", i, "address", orderedTransmitters[i])
			var addr aptos_sdk.AccountAddress
			if parseErr := addr.ParseStringRelaxed(orderedTransmitters[i]); parseErr != nil {
				wr.lggr.Warnw("TestingAptosWriteCap: failed to parse transmitter address, skipping", "address", orderedTransmitters[i], "err", parseErr)
				continue
			}
			failedHash, searchErr := txHashRetriever.GetFailedTransmissionHash(ctx, addr)
			if searchErr != nil {
				wr.lggr.Debugw("TestingAptosWriteCap: no matching failed tx for prior transmitter", "transmitter", orderedTransmitters[i], "position", i, "err", searchErr)
				continue
			}
			wr.lggr.Infow("TestingAptosWriteCap: found failed transmission from prior node", "transmitter", orderedTransmitters[i], "position", i, "txHash", failedHash)
			return &aptoscap.WriteReportReply{
				TxStatus: aptoscap.TxStatus_TX_STATUS_FATAL,
				TxHash:   &failedHash,
			}, nil
		}

		// No matching failed tx from prior nodes; return our own hash.
		wr.lggr.Infow("TestingAptosWriteCap: no prior failed tx found, returning own hash", "txHash", ownTxHash)
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_FATAL,
			TxHash:   &ownTxHash,
		}, nil
	}
	wr.lggr.Errorw("TestingAptosWriteCap: unexpected transmission state after submit")
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
	s.lggr.Infow("TestingAptosWriteCap: validateWriteReportInputs called",
		"hasRequest", request != nil,
		"workflowExecutionID", requestMetadata.WorkflowExecutionID,
	)
	if request == nil {
		s.lggr.Errorw("TestingAptosWriteCap: nil WriteReportRequest")
		return fmt.Errorf("nil WriteReportRequest")
	}
	if request.Report == nil {
		s.lggr.Errorw("TestingAptosWriteCap: nil Report in WriteReportRequest")
		return fmt.Errorf("nil Report in WriteReportRequest")
	}
	if len(request.Report.Sigs) == 0 {
		s.lggr.Errorw("TestingAptosWriteCap: no signatures provided")
		return fmt.Errorf("no signatures provided")
	}

	reportMetadata, err := decodeReportMetadata(request.Report.RawReport)
	if err != nil {
		s.lggr.Errorw("TestingAptosWriteCap: decodeReportMetadata failed", "error", err)
		return err
	}
	s.lggr.Infow("TestingAptosWriteCap: report metadata decoded",
		"version", reportMetadata.Version,
		"executionID", reportMetadata.ExecutionID,
		"workflowOwner", reportMetadata.WorkflowOwner,
		"workflowID", reportMetadata.WorkflowID,
		"reportID", reportMetadata.ReportID,
	)

	if reportMetadata.Version != 1 {
		s.lggr.Errorw("TestingAptosWriteCap: unsupported report version", "version", reportMetadata.Version)
		return fmt.Errorf("unsupported report version: %d", reportMetadata.Version)
	}

	if reportMetadata.ExecutionID != requestMetadata.WorkflowExecutionID {
		s.lggr.Errorw("TestingAptosWriteCap: workflowExecutionID mismatch",
			"reportExecutionID", reportMetadata.ExecutionID, "requestExecutionID", requestMetadata.WorkflowExecutionID)
		return fmt.Errorf("workflowExecutionID mismatch: report=%s, request=%s",
			reportMetadata.ExecutionID, requestMetadata.WorkflowExecutionID)
	}

	if !strings.EqualFold(reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner) {
		s.lggr.Errorw("TestingAptosWriteCap: workflowOwner mismatch",
			"reportOwner", reportMetadata.WorkflowOwner, "requestOwner", requestMetadata.WorkflowOwner)
		return fmt.Errorf("workflowOwner mismatch: report=%s, request=%s",
			reportMetadata.WorkflowOwner, requestMetadata.WorkflowOwner)
	}

	if reportMetadata.WorkflowID != requestMetadata.WorkflowID {
		s.lggr.Errorw("TestingAptosWriteCap: workflowID mismatch",
			"reportWorkflowID", reportMetadata.WorkflowID, "requestWorkflowID", requestMetadata.WorkflowID)
		return fmt.Errorf("workflowID mismatch: report=%s, request=%s",
			reportMetadata.WorkflowID, requestMetadata.WorkflowID)
	}

	s.lggr.Infow("TestingAptosWriteCap: all validations passed")
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
	wr.lggr.Infow("TestingAptosWriteCap: pollTransmissionInfo called",
		"transmissionID", transmissionID.GetDebugID(),
		"queuePosition", queuePosition,
		"deltaStage", wr.transmissionScheduler.deltaStage,
	)

	if queuePosition <= 0 {
		wr.lggr.Infow("TestingAptosWriteCap: position 0, doing quick retry poll")
		transmissionInfo, err := withQuickRetry(ctx, wr.lggr, func(ctx context.Context) (TransmissionInfo, error) {
			return wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		})
		if err != nil {
			wr.lggr.Errorw("TestingAptosWriteCap: quick retry poll failed", "error", err)
			return TransmissionInfo{}, err
		}
		wr.lggr.Infow("TestingAptosWriteCap: quick retry poll result", "success", transmissionInfo.Success)
		return transmissionInfo, nil
	}

	delay := time.Duration(queuePosition) * wr.transmissionScheduler.deltaStage
	wr.lggr.Infow("TestingAptosWriteCap: polling until slot or state change", "delay", delay, "deltaStage", wr.transmissionScheduler.deltaStage)

	attempt := 0
	stageTimer := time.NewTimer(delay)
	stageTimerFired := false
	defer func() {
		stageTimer.Stop()
		if !stageTimerFired {
			wr.lggr.Infow("TestingAptosWriteCap: transmission found before delta stage has passed")
		}
	}()

	for {
		if info, infoErr := wr.forwarderClient.GetTransmissionInfo(ctx, transmissionID); infoErr != nil {
			wr.lggr.Debugw("TestingAptosWriteCap: GetTransmissionInfo failed during polling", "error", infoErr, "attempt", attempt)
		} else {
			lastValidInfo = info
			if lastValidInfo.Success {
				wr.lggr.Infow("TestingAptosWriteCap: found successful transmission during polling", "attempt", attempt, "transmitter", lastValidInfo.Transmitter.String())
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
			wr.lggr.Errorw("TestingAptosWriteCap: timed out waiting for transmission info", "attempts", attempt)
			return TransmissionInfo{}, fmt.Errorf("timed out waiting for transmission info")
		case <-stageTimer.C:
			stageTimerFired = true
			wr.lggr.Infow("TestingAptosWriteCap: delta stage has passed, returning transmission info", "success", lastValidInfo.Success, "attempts", attempt)
			return lastValidInfo, nil
		case <-time.After(wait):
		}
	}
}
