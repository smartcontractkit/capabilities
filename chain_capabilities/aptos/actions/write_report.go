package actions

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/jpillora/backoff"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
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

func (s *Aptos) executeWriteReport(
	ctx context.Context,
	request *aptoscap.WriteReportRequest,
	metadata capabilities.RequestMetadata,
) (*aptoscap.WriteReportReply, error) {

	// evm
	// get transmission id
	// set gas limits, err if request gas limit is > configured limit
	// get transmission info using aptosService view method
	// find out how much transmission info aptos exposes and how much do i need
	// switch case on transmission info
	// if not attempted, continue
	// if succeeded, retrieve tx hash and return
	// if invalid reciever, retrieve tx hash and return
	// if failed, see if there is scope of gas bumping, bump and retry
	// submit now
	// check report size limit
	// invoke forwarder client, which calls evm service submit tx
	// try and poll for new transmission info for a bit, if cannot find return
	// if found, getFee and report metering
	// if found and success, check if it reverted on chain because some other node might have sent something as well ?
	// if found and failed, return failure of first attempt by any node.
	// to get TxHash, we find logs of the forwarder address
	// we use HeaderByNumber api from the evmservice and FilterLogs api from the evmservice
	// we fetch logs based ReportProcessed{receiver, workflowexecutionId, reportId}
	// the logs returned by the service which are returned by the rpc has the txHash baked in.

	// Set gas limits: use defaults if not provided, otherwise check against configured limit
	if request.GasConfig == nil {
		request.GasConfig = &aptoscap.GasConfig{}
		limit, limErr := s.maxGasAmountLimit.Limit(ctx)
		if limErr != nil {
			return nil, limErr
		}
		request.GasConfig.MaxGasAmount = limit
	} else {
		err := s.maxGasAmountLimit.Check(ctx, request.GasConfig.MaxGasAmount)
		if err != nil {
			return nil, fmt.Errorf("%s provided gas config exceeds limit (maxGasAmount=%d): %w", userError, request.GasConfig.MaxGasAmount, err)
		}
	}

	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return &aptoscap.WriteReportReply{}, err
	}

	transmissionInfo, err := withQuickRetry(ctx, s.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		return s.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get transmission info: %w", err)
	}

	switch transmissionInfo.Success {
	case true:
		txHash := []byte{}
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   txHash,
		}, nil
	case false: // this should tell me more, not attempted / failed / invalid receiver (this will be stored on chain)
		return nil, fmt.Errorf("transmission not attempted")
	}

	err = s.reportSizeLimit.Check(ctx, commoncfg.SizeOf(request.Report.RawReport))
	if err != nil {
		return nil, fmt.Errorf("%s report size exceeds limit: %w", userError, err)
	}

	s.lggr.Debugw("Submitting WriteReport transaction", "executionID", metadata.WorkflowExecutionID, "receiver", hex.EncodeToString(request.Receiver[:]))

	txReply, err := s.forwarderClient.InvokeOnReport(ctx, request.Receiver, request.Report, request.GasConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke forwarder report: %w", err)
	}

	if txReply == nil || txReply.PendingTransaction == nil {
		return nil, fmt.Errorf("nil transaction reply")
	}

	newTransmissionInfo, err := withPollingRetry(ctx, s.lggr, func(ctx context.Context) (TransmissionInfo, error) {
		readTransmissionInfo, readTransmissionErr := s.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if readTransmissionErr != nil {
			return TransmissionInfo{}, readTransmissionErr
		}
		return readTransmissionInfo, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed getting transmission info after node submitted the report on chain, %w", err)
	}

	s.lggr.Infow("Got final transmission status", "success", newTransmissionInfo.Success)

	switch newTransmissionInfo.Success {
	case true:
		txHash := []byte(txReply.PendingTransaction.Hash)
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   txHash,
		}, nil
	case false:
		return nil, fmt.Errorf("transmission failed")
	}
	return nil, fmt.Errorf("transmission state not expected after submit: %t", newTransmissionInfo.Success)
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
