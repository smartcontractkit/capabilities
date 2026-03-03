package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/jpillora/backoff"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocr3types "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/retry"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

const userError = "user error:"

const (
	aptosPollingRetryMaxBackoff        = 3 * time.Second
	aptosSpecConfigTransmittersListKey = "aptosTransmitters"
	aptosFailedHashCutoffLag           = uint64(2)
	aptosFailedHashAggregationMethod   = "observer-round-robin"
	aptosForwarderFailureMessage       = "forwarder contract execution failure"
	aptosReceiverFailureMessage        = "receiver contract execution failure"
)

type aptosFailureOrigin string

const (
	aptosFailureOriginForwarder aptosFailureOrigin = "forwarder"
	aptosFailureOriginReceiver  aptosFailureOrigin = "receiver"
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
			canonicalFailedHash, canonicalErr := s.resolveDeterministicFailedHash(ctx, metadata, transmissionID, localFailedHash, expectedForwarderRawReport, retryConfig.n)
			if canonicalErr != nil {
				return nil, fmt.Errorf("failed to resolve deterministic failed hash after timeout: %w", canonicalErr)
			}
			errorMsg := fmt.Sprintf("write transmission did not succeed before timeout: %s", s.failedTransmissionMessage(ctx, canonicalFailedHash))
			return newWriteFailedReply(canonicalFailedHash, errorMsg), nil
		}
		return nil, fmt.Errorf("failed waiting for successful transmission after submit: %w (failed hash resolution by receipt: %v)", err, failedHashErr)
	}

	if outcome.failedHash != "" {
		canonicalFailedHash, canonicalErr := s.resolveDeterministicFailedHash(ctx, metadata, transmissionID, outcome.failedHash, expectedForwarderRawReport, retryConfig.n)
		if canonicalErr != nil {
			return nil, fmt.Errorf("failed to resolve deterministic failed hash after onchain failure: %w", canonicalErr)
		}
		errorMsg := fmt.Sprintf("write transmission failed onchain: %s", s.failedTransmissionMessage(ctx, canonicalFailedHash))
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

func (s *Aptos) failedTransmissionMessage(ctx context.Context, txHash string) string {
	if s.failedTransmissionOrigin(ctx, txHash) == aptosFailureOriginForwarder {
		return aptosForwarderFailureMessage
	}
	return aptosReceiverFailureMessage
}

func (s *Aptos) failedTransmissionOrigin(ctx context.Context, txHash string) aptosFailureOrigin {
	if s.aptosService == nil {
		return aptosFailureOriginReceiver
	}

	normalizedHash, ok := normalizeAptosTxHashString(txHash)
	if !ok {
		return aptosFailureOriginReceiver
	}

	reply, err := s.aptosService.TransactionByHash(ctx, aptostypes.TransactionByHashRequest{Hash: normalizedHash})
	if err != nil || reply == nil || reply.Transaction == nil {
		s.lggr.Debugw("Failed to fetch failed transaction for origin classification", "hash", normalizedHash, "error", err)
		return aptosFailureOriginReceiver
	}

	decoded, err := decodeAccountUserTransaction(reply.Transaction.Data)
	if err != nil || decoded == nil {
		s.lggr.Debugw("Failed to decode failed transaction for origin classification", "hash", normalizedHash, "error", err)
		return aptosFailureOriginReceiver
	}

	if isForwarderAbortLocation(decoded.AbortLocation, decoded.VmStatus) {
		return aptosFailureOriginForwarder
	}
	if decoded.HasAbortCode && isKnownForwarderAbortCode(decoded.AbortCode) {
		return aptosFailureOriginForwarder
	}

	return aptosFailureOriginReceiver
}

func isForwarderAbortLocation(location string, vmStatus string) bool {
	candidates := []string{location, parseAbortLocationFromVMStatus(vmStatus)}
	for _, candidate := range candidates {
		if strings.Contains(strings.ToLower(strings.TrimSpace(candidate)), "::forwarder") {
			return true
		}
	}
	return false
}

func isKnownForwarderAbortCode(code uint64) bool {
	if code >= 1 && code <= 16 {
		return true
	}

	// Aptos framework wraps some module codes with an error category in the upper 16 bits.
	// Forwarder aborts are observed under invalid_argument (1) and permission_denied (5).
	category := code >> 16
	reason := code & 0xFFFF
	return reason >= 1 && reason <= 16 && (category == 1 || category == 5)
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
	roundBudget int,
) (string, error) {
	// Preferred path (EVM-like): scan failed tx candidates from the configured
	// transmitter set, then validate payload/transmission match onchain.
	// If this path is unavailable in a given environment, fall back to observer rounds.
	orderedTransmitters, transmitterErr := s.registryOrderedTransmittersForTransmission(ctx, metadata, transmissionID)
	if transmitterErr == nil && len(orderedTransmitters) > 0 {
		cutoffLedgerVersion, cutoffErr := s.consensusSelectFailedHashLedgerCutoff(ctx, metadata)
		if cutoffErr != nil {
			s.lggr.Debugw("Failed to reach shared Aptos ledger cutoff; continuing without cutoff", "error", cutoffErr)
			cutoffLedgerVersion = nil
		}

		canonicalFromTransmitters, transmitterLookupErr := s.resolveFirstValidatedFailedHashFromTransmitters(
			ctx,
			transmissionID,
			orderedTransmitters,
			expectedForwarderRawReport,
			cutoffLedgerVersion,
		)
		if transmitterLookupErr == nil && strings.TrimSpace(canonicalFromTransmitters) != "" {
			return canonicalFromTransmitters, nil
		}

		// Retry once without cutoff if the cutoff-bounded scan found no validated candidate.
		if cutoffLedgerVersion != nil {
			canonicalFromTransmitters, uncappedErr := s.resolveFirstValidatedFailedHashFromTransmitters(
				ctx,
				transmissionID,
				orderedTransmitters,
				expectedForwarderRawReport,
				nil,
			)
			if uncappedErr == nil && strings.TrimSpace(canonicalFromTransmitters) != "" {
				return canonicalFromTransmitters, nil
			}
			if uncappedErr != nil {
				s.lggr.Debugw("Uncapped registry transmitter scan failed", "error", uncappedErr)
			}
		}

		s.lggr.Debugw("Registry transmitter scan did not yield a validated failed hash; falling back to observer rounds", "error", transmitterLookupErr)
	} else if transmitterErr != nil {
		s.lggr.Debugw("Aptos transmitter set unavailable from capability registry; falling back to observer rounds", "error", transmitterErr)
	}

	return s.resolveDeterministicFailedHashByObserverRound(ctx, metadata, transmissionID, localFailedHash, expectedForwarderRawReport, roundBudget)
}

func (s *Aptos) resolveDeterministicFailedHashByObserverRound(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	transmissionID TransmissionID,
	localFailedHash string,
	expectedForwarderRawReport []byte,
	roundBudget int,
) (string, error) {
	localCandidate, err := s.resolveLocalFailedHashCandidate(ctx, transmissionID, localFailedHash, expectedForwarderRawReport)
	if err != nil {
		s.lggr.Debugw("Local failed hash candidate unavailable; entering consensus rounds with sentinel", "error", err)
		localCandidate = ""
	}

	if roundBudget <= 0 {
		roundBudget = 1
	}

	for round := 0; round < roundBudget; round++ {
		selectedHash, consensusErr := s.consensusSelectFailedHashByObserverRound(ctx, metadata, localCandidate, round)
		if consensusErr != nil {
			return "", fmt.Errorf("failed hash consensus round %d: %w", round, consensusErr)
		}
		if strings.TrimSpace(selectedHash) == "" {
			continue
		}

		// Every node re-validates the consensus-selected hash against onchain payload.
		validatedHash, validateErr := s.forwarderClient.ValidateFailedTxHash(ctx, transmissionID, selectedHash, expectedForwarderRawReport)
		if validateErr != nil {
			s.lggr.Debugw(
				"Consensus-selected failed hash did not validate; advancing to next observer round",
				"round", round,
				"selectedHash", selectedHash,
				"error", validateErr,
			)
			continue
		}
		if strings.TrimSpace(validatedHash) == "" {
			continue
		}

		return validatedHash, nil
	}

	return "", fmt.Errorf("failed to resolve deterministic failed hash after %d consensus rounds", roundBudget)
}

func (s *Aptos) resolveLocalFailedHashCandidate(
	ctx context.Context,
	transmissionID TransmissionID,
	localFailedHash string,
	expectedForwarderRawReport []byte,
) (string, error) {
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
	cutoffLedgerVersion *uint64,
) (string, error) {
	for _, transmitter := range transmitters {
		candidateHash, err := s.forwarderClient.GetTransmissionFailedTxHash(ctx, transmissionID, []string{transmitter}, cutoffLedgerVersion)
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
	transmitters, err := transmittersFromCapabilitySpecConfig(cfg.SpecConfig)
	if err != nil {
		return nil, err
	}

	// Keep configured order so failed-hash scanning follows the same canonical
	// transmitter ordering that nodes are configured with.
	return transmitters, nil
}

func transmittersFromCapabilitySpecConfig(specConfig *values.Map) ([]string, error) {
	if specConfig == nil {
		return nil, fmt.Errorf("capability spec config is missing")
	}

	rawValue, ok := specConfig.Underlying[aptosSpecConfigTransmittersListKey]
	if !ok {
		return nil, fmt.Errorf("capability spec config missing %q", aptosSpecConfigTransmittersListKey)
	}

	var transmitters []string
	if err := rawValue.UnwrapTo(&transmitters); err != nil {
		return nil, fmt.Errorf("invalid %q in capability spec config: %w", aptosSpecConfigTransmittersListKey, err)
	}

	transmitters = orderedUniqueTransmitters(transmitters)
	if len(transmitters) == 0 {
		return nil, fmt.Errorf("capability spec config %q is empty", aptosSpecConfigTransmittersListKey)
	}

	return transmitters, nil
}

func (s *Aptos) consensusSelectFailedHashByObserverRound(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	localFailedHash string,
	round int,
) (string, error) {
	requestID := fmt.Sprintf("%s:aptos-failed-hash:round:%d", commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID), round)

	request := ctypes.NewAggregatableRequest(requestID, func(ctx context.Context) (*ctypes.AggregatableObservation, error) {
		candidateHash := strings.TrimSpace(localFailedHash)
		if candidateHash == "" {
			return &ctypes.AggregatableObservation{
				Method: aptosFailedHashAggregationMethod,
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
			Method: aptosFailedHashAggregationMethod,
			Value:  value,
		}, nil
	})

	decimalValue, err := readType[*pb.Decimal](ctx, s.ConsensusHandler, request)
	if err != nil {
		return "", err
	}

	hash, noCandidate, err := decimalToAptosHashOrSentinel(decimalValue)
	if err != nil {
		return "", err
	}
	if noCandidate {
		return "", nil
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

func decimalToAptosHashOrSentinel(value *pb.Decimal) (string, bool, error) {
	if value == nil || value.Coefficient == nil {
		return "", false, fmt.Errorf("nil decimal coefficient")
	}
	// Exponent 1 with zero coefficient is used as sentinel for "no local failed hash candidate".
	if value.Exponent == 1 && pb.NewIntFromBigInt(value.Coefficient).Sign() == 0 {
		return "", true, nil
	}

	hash, err := decimalToAptosHash(value)
	if err != nil {
		return "", false, err
	}
	return hash, false, nil
}

func (s *Aptos) consensusSelectFailedHashLedgerCutoff(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
) (*uint64, error) {
	if s.aptosService == nil {
		return nil, fmt.Errorf("aptos service unavailable")
	}

	requestID := commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID) + ":aptos-failed-hash-cutoff"
	request := ctypes.NewAggregatableRequest(requestID, func(ctx context.Context) (*ctypes.AggregatableObservation, error) {
		latestLedgerVersion, err := s.aptosService.LedgerVersion(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read latest ledger version: %w", err)
		}
		return &ctypes.AggregatableObservation{
			Method: ctypes.AggregationMethodFPlusOneHighest,
			Value:  uint64ToInvertedDecimal(latestLedgerVersion),
		}, nil
	})

	decimalValue, err := readType[*pb.Decimal](ctx, s.ConsensusHandler, request)
	if err != nil {
		return nil, err
	}

	agreedLedgerVersion, err := invertedDecimalToUint64(decimalValue)
	if err != nil {
		return nil, err
	}

	cutoffLedgerVersion := uint64(0)
	if agreedLedgerVersion > aptosFailedHashCutoffLag {
		cutoffLedgerVersion = agreedLedgerVersion - aptosFailedHashCutoffLag
	}

	return &cutoffLedgerVersion, nil
}

func uint64ToInvertedDecimal(value uint64) *pb.Decimal {
	inverted := new(big.Int).SetUint64(math.MaxUint64 - value)
	return &pb.Decimal{
		Coefficient: pb.NewBigIntFromInt(inverted),
		Exponent:    0,
	}
}

func invertedDecimalToUint64(value *pb.Decimal) (uint64, error) {
	if value == nil || value.Coefficient == nil {
		return 0, fmt.Errorf("nil decimal coefficient")
	}
	if value.Exponent != 0 {
		return 0, fmt.Errorf("unexpected decimal exponent %d for ledger version", value.Exponent)
	}

	intValue := pb.NewIntFromBigInt(value.Coefficient)
	if intValue.Sign() < 0 {
		return 0, fmt.Errorf("negative ledger version value")
	}
	if intValue.BitLen() > 64 {
		return 0, fmt.Errorf("ledger version exceeds uint64")
	}

	inverted := intValue.Uint64()
	return math.MaxUint64 - inverted, nil
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
