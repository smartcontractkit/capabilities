package actions

import (
	"context"
	"encoding/hex"
	"errors"
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

const (
	aptosPollingRetryMaxBackoff = 3 * time.Second
)

type aptosWriteRetryConfig struct {
	timeout    time.Duration
	maxBackoff time.Duration
	maxRetries uint
	f          int
	n          int
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

	// Set gas limits: use defaults if not provided (or provided as zero), otherwise check against configured limit.
	if request.GasConfig == nil {
		request.GasConfig = &aptoscap.GasConfig{}
	}

	if request.GasConfig.MaxGasAmount == 0 {
		limit, limErr := s.maxGasAmountLimit.Limit(ctx)
		if limErr != nil {
			return nil, limErr
		}
		request.GasConfig.MaxGasAmount = limit
	} else {
		if err := s.maxGasAmountLimit.Check(ctx, request.GasConfig.MaxGasAmount); err != nil {
			return nil, fmt.Errorf("%s provided gas config exceeds limit (maxGasAmount=%d): %w", userError, request.GasConfig.MaxGasAmount, err)
		}
	}

	transmissionID, err := getTransmissionID(metadata.WorkflowExecutionID, request)
	if err != nil {
		return &aptoscap.WriteReportReply{}, err
	}
	retryConfig := deriveAptosWriteRetryConfig(len(request.Report.Sigs))
	retryConfig = applyContextTimeoutToAptosWriteRetryConfig(ctx, retryConfig)
	s.lggr.Debugw("Aptos write retry policy", "f", retryConfig.f, "n", retryConfig.n, "timeout", retryConfig.timeout.String(), "maxBackoff", retryConfig.maxBackoff.String())

	transmissionInfo, err := s.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get transmission info: %w", err)
	}

	if transmissionInfo.Success {
		s.lggr.Infow("Transmission already confirmed onchain before submit", "transmitter", transmissionInfo.Transmitter)
		if transmissionInfo.Transmitter == "" {
			return nil, fmt.Errorf("successful transmission has no transmitter")
		}
		canonicalHash, hashErr := s.forwarderClient.GetTransmissionTxHash(ctx, transmissionID, transmissionInfo.Transmitter)
		if hashErr != nil {
			return nil, fmt.Errorf("failed to resolve canonical tx hash for pre-existing transmission: %w", hashErr)
		}
		if canonicalHash == "" {
			return nil, fmt.Errorf("canonical tx hash for pre-existing transmission is empty")
		}
		txHash := []byte(canonicalHash)
		return &aptoscap.WriteReportReply{
			TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
			TxHash:   txHash,
		}, nil
	}
	// transmission not present yet; continue to submit.

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

	localSubmittedHash := strings.TrimSpace(txReply.PendingTransaction.Hash)
	ourSender := normalizeAptosHexAddress(hex.EncodeToString(txReply.PendingTransaction.Sender[:]))
	newTransmissionInfo, err := s.waitForTransmissionSuccess(ctx, transmissionID, retryConfig)
	if err != nil {
		failedHash, failedHashErr := s.waitForFailedTransmissionHashByHash(ctx, transmissionID, localSubmittedHash, retryConfig)
		if failedHashErr == nil {
			errorMsg := fmt.Sprintf("write transmission did not succeed before timeout: %v", err)
			return &aptoscap.WriteReportReply{
				TxStatus:     aptoscap.TxStatus_TX_STATUS_FAILED,
				TxHash:       []byte(failedHash),
				ErrorMessage: &errorMsg,
			}, nil
		}
		return nil, fmt.Errorf("failed waiting for successful transmission after submit: %w (failed hash resolution by receipt: %v)", err, failedHashErr)
	}

	s.lggr.Infow("Got final transmission status", "success", newTransmissionInfo.Success)

	submittedHash := txReply.PendingTransaction.Hash
	onchainTransmitter := normalizeAptosHexAddress(newTransmissionInfo.Transmitter)
	if onchainTransmitter == "" {
		return nil, fmt.Errorf("successful transmission has no transmitter")
	}
	hash, hashErr := s.forwarderClient.GetTransmissionTxHash(ctx, transmissionID, onchainTransmitter)
	if hashErr != nil {
		return nil, fmt.Errorf("failed to resolve canonical tx hash from winning transmitter: %w", hashErr)
	}
	if hash == "" {
		return nil, fmt.Errorf("canonical tx hash from winning transmitter is empty")
	}
	submittedHash = hash
	if ourSender != "" && onchainTransmitter != "" && ourSender != onchainTransmitter {
		s.lggr.Infow("Report was confirmed onchain by another transmitter", "ourSender", ourSender, "onchainTransmitter", onchainTransmitter, "canonicalTxHash", submittedHash)
	}

	txHash := []byte(submittedHash)
	return &aptoscap.WriteReportReply{
		TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
		TxHash:   txHash,
	}, nil
}

func (s *Aptos) waitForTransmissionSuccess(ctx context.Context, transmissionID TransmissionID, retryConfig aptosWriteRetryConfig) (TransmissionInfo, error) {
	return withAptosPollingRetry(ctx, s.lggr, retryConfig, func(ctx context.Context) (TransmissionInfo, error) {
		info, err := s.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if err != nil {
			return TransmissionInfo{}, err
		}
		if info.Success {
			return info, nil
		}
		return TransmissionInfo{}, errors.New("transmission not yet successful, retrying")
	})
}

func (s *Aptos) waitForFailedTransmissionHash(ctx context.Context, transmissionID TransmissionID, candidateTransmitters []string, retryConfig aptosWriteRetryConfig) (string, error) {
	if len(candidateTransmitters) == 0 {
		return "", fmt.Errorf("no candidate transmitters available for failed hash resolution")
	}

	return withAptosPollingRetry(ctx, s.lggr, retryConfig, func(ctx context.Context) (string, error) {
		hash, err := s.forwarderClient.GetTransmissionFailedTxHash(ctx, transmissionID, candidateTransmitters)
		if err != nil {
			return "", err
		}
		if hash == "" {
			return "", errors.New("empty failed tx hash")
		}
		return hash, nil
	})
}

func (s *Aptos) waitForFailedTransmissionHashByHash(ctx context.Context, transmissionID TransmissionID, txHash string, retryConfig aptosWriteRetryConfig) (string, error) {
	if strings.TrimSpace(txHash) == "" {
		return "", fmt.Errorf("empty submitted tx hash")
	}

	return withAptosPollingRetry(ctx, s.lggr, retryConfig, func(ctx context.Context) (string, error) {
		hash, err := s.forwarderClient.ValidateFailedTxHash(ctx, transmissionID, txHash)
		if err != nil {
			return "", err
		}
		if hash == "" {
			return "", errors.New("empty failed tx hash")
		}
		return hash, nil
	})
}

func withAptosPollingRetry[T any](ctx context.Context, lggr logger.Logger, retryConfig aptosWriteRetryConfig, fn func(context.Context) (T, error)) (T, error) {
	return withAptosRetry(ctx, lggr, fn, retryConfig.timeout, retryConfig.maxBackoff, retryConfig.maxRetries)
}

func withAptosRetry[T any](
	ctx context.Context,
	lggr logger.Logger,
	fn func(context.Context) (T, error),
	timeout time.Duration,
	maxBackoff time.Duration,
	maxRetries uint,
) (T, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	} else if _, hasDeadline := ctx.Deadline(); !hasDeadline && maxRetries == 0 {
		var zero T
		return zero, fmt.Errorf("retry timeout is not configured: context has no deadline and maxRetries is 0")
	}

	var lastErr error
	strategy := retry.Strategy[T]{
		Backoff:    &backoff.Backoff{Factor: 2, Min: 100 * time.Millisecond, Max: maxBackoff},
		MaxRetries: maxRetries,
	}

	result, err := strategy.Do(ctx, lggr, func(ctx context.Context) (T, error) {
		r, fnErr := fn(ctx)
		if fnErr != nil {
			lastErr = fnErr
		}
		return r, fnErr
	})
	if err != nil {
		if lastErr != nil {
			return result, lastErr
		}
		return result, err
	}
	return result, nil
}

func deriveAptosWriteRetryConfig(sigCount int) aptosWriteRetryConfig {
	// OCR report carries exactly f+1 signatures.
	fPlusOne := sigCount
	if fPlusOne < 1 {
		fPlusOne = 1
	}
	f := fPlusOne - 1
	if f < 1 {
		f = 1
	}
	// In OCR, N is typically 3f+1.
	n := 3*f + 1
	return aptosWriteRetryConfig{
		// timeout is derived from request context deadline, which is configured offchain.
		timeout:    0,
		maxBackoff: aptosPollingRetryMaxBackoff,
		// 0 => retry until context timeout.
		maxRetries: 0,
		f:          f,
		n:          n,
	}
}

func applyContextTimeoutToAptosWriteRetryConfig(ctx context.Context, cfg aptosWriteRetryConfig) aptosWriteRetryConfig {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			cfg.timeout = remaining
			return cfg
		}
		cfg.timeout = 1 * time.Millisecond
	}
	return cfg
}

func normalizeAptosHexAddress(input string) string {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimLeft(s, "0")
	if s == "" {
		return "0x0"
	}
	return "0x" + s
}

func normalizeAptosTxHashString(input string) (string, bool) {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return "", false
	}
	s = strings.TrimPrefix(s, "0x")
	if len(s) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return "0x" + s, true
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
