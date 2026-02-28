package actions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/jpillora/backoff"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocr3types "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/retry"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
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

type aptosWriteOutcome struct {
	transmissionInfo TransmissionInfo
	failedHash       string
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
	expectedForwarderRawReport, err := expectedForwarderRawReportBytes(request.Report)
	if err != nil {
		return nil, fmt.Errorf("invalid report payload for failed-hash validation: %w", err)
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
		canonicalHash, err := s.waitForCanonicalSuccessTxHash(ctx, transmissionID, transmissionInfo.Transmitter, expectedForwarderRawReport, retryConfig, "pre-existing transmission")
		if err != nil {
			return nil, err
		}
		return newWriteSuccessReply(canonicalHash), nil
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
	outcome, err := s.waitForTransmissionOutcome(ctx, transmissionID, localSubmittedHash, expectedForwarderRawReport, retryConfig)
	if err != nil {
		localFailedHash, failedHashErr := s.waitForFailedTransmissionHashByHash(ctx, transmissionID, localSubmittedHash, expectedForwarderRawReport, retryConfig)
		if failedHashErr == nil {
			canonicalFailedHash, canonicalErr := s.resolveDeterministicFailedHash(ctx, metadata, transmissionID, localFailedHash, expectedForwarderRawReport)
			if canonicalErr != nil {
				return nil, fmt.Errorf("failed to resolve deterministic failed hash after timeout: %w", canonicalErr)
			}
			errorMsg := fmt.Sprintf("write transmission did not succeed before timeout: %v", err)
			return newWriteFailedReply(canonicalFailedHash, errorMsg), nil
		}
		return nil, fmt.Errorf("failed waiting for successful transmission after submit: %w (failed hash resolution by receipt: %v)", err, failedHashErr)
	}

	if outcome.failedHash != "" {
		canonicalFailedHash, canonicalErr := s.resolveDeterministicFailedHash(ctx, metadata, transmissionID, outcome.failedHash, expectedForwarderRawReport)
		if canonicalErr != nil {
			return nil, fmt.Errorf("failed to resolve deterministic failed hash after onchain failure: %w", canonicalErr)
		}
		errorMsg := "write transmission failed onchain"
		return newWriteFailedReply(canonicalFailedHash, errorMsg), nil
	}

	newTransmissionInfo := outcome.transmissionInfo
	s.lggr.Infow("Got final transmission status", "success", newTransmissionInfo.Success)

	submittedHash := txReply.PendingTransaction.Hash
	onchainTransmitter := normalizeAptosHexAddress(newTransmissionInfo.Transmitter)
	canonicalHash, err := s.waitForCanonicalSuccessTxHash(ctx, transmissionID, onchainTransmitter, expectedForwarderRawReport, retryConfig, "winning transmitter")
	if err != nil {
		return nil, err
	}
	submittedHash = canonicalHash
	if ourSender != "" && onchainTransmitter != "" && ourSender != onchainTransmitter {
		s.lggr.Infow("Report was confirmed onchain by another transmitter", "ourSender", ourSender, "onchainTransmitter", onchainTransmitter, "canonicalTxHash", submittedHash)
	}

	return newWriteSuccessReply(submittedHash), nil
}

func newWriteSuccessReply(txHash string) *aptoscap.WriteReportReply {
	return &aptoscap.WriteReportReply{
		TxStatus: aptoscap.TxStatus_TX_STATUS_SUCCESS,
		TxHash:   []byte(txHash),
	}
}

func newWriteFailedReply(txHash string, errorMsg string) *aptoscap.WriteReportReply {
	return &aptoscap.WriteReportReply{
		TxStatus:     aptoscap.TxStatus_TX_STATUS_FAILED,
		TxHash:       []byte(txHash),
		ErrorMessage: &errorMsg,
	}
}

func (s *Aptos) resolveCanonicalSuccessTxHash(
	ctx context.Context,
	transmissionID TransmissionID,
	transmitter string,
	expectedForwarderRawReport []byte,
	contextLabel string,
) (string, error) {
	if strings.TrimSpace(transmitter) == "" {
		return "", fmt.Errorf("successful transmission has no transmitter")
	}
	canonicalHash, err := s.forwarderClient.GetTransmissionTxHash(ctx, transmissionID, transmitter, expectedForwarderRawReport)
	if err != nil {
		return "", fmt.Errorf("failed to resolve canonical tx hash for %s: %w", contextLabel, err)
	}
	if canonicalHash == "" {
		return "", fmt.Errorf("canonical tx hash for %s is empty", contextLabel)
	}
	return canonicalHash, nil
}

func (s *Aptos) waitForCanonicalSuccessTxHash(
	ctx context.Context,
	transmissionID TransmissionID,
	transmitter string,
	expectedForwarderRawReport []byte,
	retryConfig aptosWriteRetryConfig,
	contextLabel string,
) (string, error) {
	return withAptosPollingRetry(ctx, s.lggr, retryConfig, func(ctx context.Context) (string, error) {
		return s.resolveCanonicalSuccessTxHash(ctx, transmissionID, transmitter, expectedForwarderRawReport, contextLabel)
	})
}

func (s *Aptos) resolveDeterministicFailedHash(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	transmissionID TransmissionID,
	localFailedHash string,
	expectedForwarderRawReport []byte,
) (string, error) {
	localCandidate, err := s.resolveLocalFailedHashCandidate(ctx, metadata, transmissionID, localFailedHash, expectedForwarderRawReport)
	if err != nil {
		return "", fmt.Errorf("local failed hash resolution: %w", err)
	}

	selectedHash, err := s.consensusSelectFailedHash(ctx, metadata, localCandidate)
	if err != nil {
		return "", fmt.Errorf("failed hash consensus: %w", err)
	}

	// Every node re-validates the consensus-selected hash against onchain payload.
	validatedHash, err := s.forwarderClient.ValidateFailedTxHash(ctx, transmissionID, selectedHash, expectedForwarderRawReport)
	if err != nil {
		return "", fmt.Errorf("consensus-selected failed hash did not validate onchain: %w", err)
	}
	if strings.TrimSpace(validatedHash) == "" {
		return "", fmt.Errorf("consensus-selected failed hash is empty after validation")
	}
	return validatedHash, nil
}

func (s *Aptos) resolveLocalFailedHashCandidate(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	transmissionID TransmissionID,
	localFailedHash string,
	expectedForwarderRawReport []byte,
) (string, error) {
	orderedTransmitters, txErr := s.registryOrderedTransmittersForTransmission(ctx, metadata, transmissionID)
	if txErr == nil && len(orderedTransmitters) > 0 {
		candidateHash, err := s.resolveFirstValidatedFailedHashFromTransmitters(ctx, transmissionID, orderedTransmitters, expectedForwarderRawReport)
		if err == nil {
			return candidateHash, nil
		}
		s.lggr.Debugw("Registry-backed failed hash resolution unavailable; falling back to local failed hash", "error", err)
	} else if txErr != nil {
		s.lggr.Debugw("Failed to load registry transmitters for failed hash resolution; falling back to local failed hash", "error", txErr)
	}

	localCandidate := strings.TrimSpace(localFailedHash)
	if localCandidate == "" {
		return "", fmt.Errorf("local failed hash is empty")
	}

	validatedHash, err := s.forwarderClient.ValidateFailedTxHash(ctx, transmissionID, localCandidate, expectedForwarderRawReport)
	if err != nil {
		return "", fmt.Errorf("local failed hash did not validate onchain: %w", err)
	}
	if strings.TrimSpace(validatedHash) == "" {
		return "", fmt.Errorf("local failed hash is empty after validation")
	}
	return validatedHash, nil
}

func (s *Aptos) resolveFirstValidatedFailedHashFromTransmitters(
	ctx context.Context,
	transmissionID TransmissionID,
	transmitters []string,
	expectedForwarderRawReport []byte,
) (string, error) {
	for _, transmitter := range transmitters {
		candidateHash, err := s.forwarderClient.GetTransmissionFailedTxHash(ctx, transmissionID, []string{transmitter})
		if err != nil {
			s.lggr.Debugw("Failed hash lookup for transmitter failed", "transmitter", transmitter, "error", err)
			continue
		}
		candidateHash = strings.TrimSpace(candidateHash)
		if candidateHash == "" {
			continue
		}

		validatedHash, err := s.forwarderClient.ValidateFailedTxHash(ctx, transmissionID, candidateHash, expectedForwarderRawReport)
		if err != nil {
			s.lggr.Debugw("Failed hash candidate did not validate", "transmitter", transmitter, "candidateHash", candidateHash, "error", err)
			continue
		}
		validatedHash = strings.TrimSpace(validatedHash)
		if validatedHash == "" {
			continue
		}

		return validatedHash, nil
	}

	return "", fmt.Errorf("no validated failed tx hash found from registry transmitters")
}

func (s *Aptos) registryOrderedTransmittersForTransmission(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	transmissionID TransmissionID,
) ([]string, error) {
	if s.capabilityRegistry == nil {
		return nil, fmt.Errorf("capability registry unavailable")
	}
	if s.capabilityID == "" {
		return nil, fmt.Errorf("capability id unavailable")
	}
	if metadata.WorkflowDonID == 0 {
		return nil, fmt.Errorf("workflow DON id unavailable")
	}

	cfg, err := s.capabilityRegistry.ConfigForCapability(ctx, s.capabilityID, metadata.WorkflowDonID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch capability config: %w", err)
	}
	if len(cfg.Ocr3Configs) == 0 {
		return nil, fmt.Errorf("capability config has no OCR3 config")
	}

	ocr3Config, err := selectRegistryOCR3Config(cfg.Ocr3Configs)
	if err != nil {
		return nil, err
	}

	transmitters := make([]string, 0, len(ocr3Config.Transmitters))
	for _, transmitter := range ocr3Config.Transmitters {
		normalized := normalizeAptosHexAddress(string(transmitter))
		if normalized == "" {
			continue
		}
		transmitters = append(transmitters, normalized)
	}
	if len(transmitters) == 0 {
		return nil, fmt.Errorf("OCR3 transmitters list is empty")
	}

	return canonicalTransmitterOrderForTransmission(transmitters, transmissionID), nil
}

func selectRegistryOCR3Config(ocr3Configs map[string]ocrtypes.ContractConfig) (ocrtypes.ContractConfig, error) {
	if cfg, ok := ocr3Configs[capabilitiespb.OCR3ConfigDefaultKey]; ok {
		return cfg, nil
	}

	if len(ocr3Configs) == 1 {
		for _, cfg := range ocr3Configs {
			return cfg, nil
		}
	}

	keys := make([]string, 0, len(ocr3Configs))
	for key := range ocr3Configs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ocrtypes.ContractConfig{}, fmt.Errorf("missing OCR3 config keys")
	}
	return ocr3Configs[keys[0]], nil
}

func canonicalTransmitterOrderForTransmission(transmitters []string, transmissionID TransmissionID) []string {
	unique := orderedUniqueTransmitters(transmitters)
	if len(unique) == 0 {
		return nil
	}

	seed := failedHashOrderSeed(transmissionID)
	type rankedTransmitter struct {
		transmitter string
		rank        [32]byte
	}

	ranked := make([]rankedTransmitter, 0, len(unique))
	for _, transmitter := range unique {
		seedInput := make([]byte, 0, len(seed)+len(transmitter))
		seedInput = append(seedInput, seed...)
		seedInput = append(seedInput, transmitter...)
		ranked = append(ranked, rankedTransmitter{
			transmitter: transmitter,
			rank:        sha256.Sum256(seedInput),
		})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if cmp := bytes.Compare(ranked[i].rank[:], ranked[j].rank[:]); cmp != 0 {
			return cmp < 0
		}
		return ranked[i].transmitter < ranked[j].transmitter
	})

	ordered := make([]string, 0, len(ranked))
	for _, candidate := range ranked {
		ordered = append(ordered, candidate.transmitter)
	}
	return ordered
}

func failedHashOrderSeed(transmissionID TransmissionID) []byte {
	seed := make([]byte, 0, len(transmissionID.WorkflowExecutionID)+len(transmissionID.ReportID)+len(transmissionID.Receiver))
	seed = append(seed, transmissionID.WorkflowExecutionID[:]...)
	seed = append(seed, transmissionID.ReportID[:]...)
	seed = append(seed, transmissionID.Receiver[:]...)
	return seed
}

func (s *Aptos) consensusSelectFailedHash(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	localFailedHash string,
) (string, error) {
	requestID := commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID) + ":aptos-failed-hash"

	request := ctypes.NewAggregatableRequest(requestID, func(ctx context.Context) (*ctypes.AggregatableObservation, error) {
		candidateHash := strings.TrimSpace(localFailedHash)
		if candidateHash == "" {
			return &ctypes.AggregatableObservation{
				Method: ctypes.AggregationMethodFPlusOneHighest,
				// Exponent != 0 marks "no local candidate hash".
				Value: &pb.Decimal{
					Coefficient: pb.NewBigIntFromInt(big.NewInt(0)),
					Exponent:    1,
				},
			}, nil
		}

		value, err := aptosHashToDecimal(candidateHash)
		if err != nil {
			return nil, err
		}

		return &ctypes.AggregatableObservation{
			Method: ctypes.AggregationMethodFPlusOneHighest,
			Value:  value,
		}, nil
	})

	decimalValue, err := readType[*pb.Decimal](ctx, s.ConsensusHandler, request)
	if err != nil {
		return "", err
	}

	hash, err := decimalToAptosHash(decimalValue)
	if err != nil {
		return "", err
	}

	return hash, nil
}

func aptosHashToDecimal(hash string) (*pb.Decimal, error) {
	normalizedHash, ok := normalizeAptosTxHashString(hash)
	if !ok {
		return nil, fmt.Errorf("invalid tx hash %q", hash)
	}

	normalizedHash = strings.TrimPrefix(normalizedHash, "0x")

	value := new(big.Int)
	if _, ok := value.SetString(normalizedHash, 16); !ok {
		return nil, fmt.Errorf("failed to parse tx hash %q as uint256", hash)
	}

	return &pb.Decimal{
		Coefficient: pb.NewBigIntFromInt(value),
		Exponent:    0,
	}, nil
}

func decimalToAptosHash(value *pb.Decimal) (string, error) {
	if value == nil || value.Coefficient == nil {
		return "", fmt.Errorf("nil decimal coefficient")
	}
	if value.Exponent != 0 {
		return "", fmt.Errorf("unexpected decimal exponent %d for tx hash", value.Exponent)
	}

	intValue := pb.NewIntFromBigInt(value.Coefficient)
	if intValue.Sign() < 0 {
		return "", fmt.Errorf("negative tx hash value")
	}

	hexHash := fmt.Sprintf("%x", intValue)
	if len(hexHash) > 64 {
		return "", fmt.Errorf("tx hash exceeds 256 bits")
	}
	if len(hexHash) < 64 {
		hexHash = strings.Repeat("0", 64-len(hexHash)) + hexHash
	}

	return "0x" + strings.ToLower(hexHash), nil
}

func expectedForwarderRawReportBytes(report *sdk.ReportResponse) ([]byte, error) {
	if report == nil {
		return nil, fmt.Errorf("nil report")
	}
	if len(report.RawReport) == 0 {
		return nil, fmt.Errorf("empty raw report")
	}

	rawReport := append([]byte(nil), report.RawReport...)
	if len(report.ReportContext) == 0 {
		return rawReport, nil
	}
	if len(report.ReportContext) != 96 {
		return nil, fmt.Errorf("invalid report context length: got %d want 96", len(report.ReportContext))
	}

	combined := make([]byte, 0, len(report.ReportContext)+len(report.RawReport))
	combined = append(combined, report.ReportContext...)
	combined = append(combined, report.RawReport...)
	return combined, nil
}

func (s *Aptos) waitForTransmissionOutcome(
	ctx context.Context,
	transmissionID TransmissionID,
	submittedHash string,
	expectedForwarderRawReport []byte,
	retryConfig aptosWriteRetryConfig,
) (aptosWriteOutcome, error) {
	normalizedSubmittedHash := strings.TrimSpace(submittedHash)

	return withAptosPollingRetry(ctx, s.lggr, retryConfig, func(ctx context.Context) (aptosWriteOutcome, error) {
		info, infoErr := s.forwarderClient.GetTransmissionInfo(ctx, transmissionID)
		if infoErr == nil && info.Success {
			return aptosWriteOutcome{transmissionInfo: info}, nil
		}

		if normalizedSubmittedHash != "" {
			failedHash, failedErr := s.forwarderClient.ValidateFailedTxHash(ctx, transmissionID, normalizedSubmittedHash, expectedForwarderRawReport)
			if failedErr == nil && strings.TrimSpace(failedHash) != "" {
				return aptosWriteOutcome{failedHash: failedHash}, nil
			}
			if infoErr != nil && failedErr != nil {
				return aptosWriteOutcome{}, fmt.Errorf("transmission not finalized yet: transmission_info_err=%v failed_hash_err=%v", infoErr, failedErr)
			}
			if failedErr != nil {
				return aptosWriteOutcome{}, fmt.Errorf("transmission not finalized yet: failed_hash_err=%v", failedErr)
			}
		}

		if infoErr != nil {
			return aptosWriteOutcome{}, fmt.Errorf("transmission info unavailable: %w", infoErr)
		}
		return aptosWriteOutcome{}, errors.New("transmission not yet successful and no failed hash observed")
	})
}

func (s *Aptos) waitForFailedTransmissionHashByHash(
	ctx context.Context,
	transmissionID TransmissionID,
	txHash string,
	expectedForwarderRawReport []byte,
	retryConfig aptosWriteRetryConfig,
) (string, error) {
	if strings.TrimSpace(txHash) == "" {
		return "", fmt.Errorf("empty submitted tx hash")
	}

	return withAptosPollingRetry(ctx, s.lggr, retryConfig, func(ctx context.Context) (string, error) {
		hash, err := s.forwarderClient.ValidateFailedTxHash(ctx, transmissionID, txHash, expectedForwarderRawReport)
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

func decodeReportMetadata(data []byte) (ocr3types.Metadata, error) {
	metadata, _, err := ocr3types.Decode(data)
	return metadata, err
}

func (s *Aptos) isUserError(err error) bool {
	return strings.HasPrefix(err.Error(), "user error:")
}
