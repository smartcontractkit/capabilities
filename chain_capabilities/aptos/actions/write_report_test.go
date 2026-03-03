package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
	"github.com/stretchr/testify/require"
)

func TestExecuteWriteReport_ReturnsDeterministicFailedHashOnInvalidCall(t *testing.T) {
	transmissionID := newTestTransmissionID()
	rawReport := mustEncodedReportWithMetadata(t, transmissionID)

	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		WorkflowOwner:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WorkflowID:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	receiver := make([]byte, len(transmissionID.Receiver))
	copy(receiver, transmissionID.Receiver[:])

	sig := &sdk.AttributedSignature{Signature: []byte{1}, SignerId: 0}
	request := &aptoscap.WriteReportRequest{
		Receiver: receiver,
		Report: &sdk.ReportResponse{
			RawReport: rawReport,
			Sigs:      []*sdk.AttributedSignature{sig},
		},
		GasConfig: &aptoscap.GasConfig{
			MaxGasAmount: 1000,
		},
	}

	maxGasLimiter, err := limits.MakeBoundLimiter(limits.Factory{}, settings.Uint64(1_000_000))
	require.NoError(t, err)
	reportSizeLimiter, err := limits.MakeBoundLimiter(limits.Factory{}, settings.Size(commoncfg.Byte*512))
	require.NoError(t, err)

	mockForwarder := &mockForwarderClient{
		pendingSender: transmissionID.Receiver,
		pendingHash:   "0x" + strings.Repeat("1", 64),
		failedHash:    "0x" + strings.Repeat("f", 64),
	}

	a := &Aptos{
		forwarderClient:   mockForwarder,
		ConsensusHandler:  &passthroughAggregatableConsensusHandler{},
		lggr:              logger.Sugared(logger.Test(t)),
		maxGasAmountLimit: maxGasLimiter,
		reportSizeLimit:   reportSizeLimiter,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	reply, err := a.executeWriteReport(ctx, request, metadata)
	require.NoError(t, err)
	require.NotNil(t, reply)
	require.Equal(t, aptoscap.TxStatus_TX_STATUS_FAILED, reply.TxStatus)
	require.Equal(t, []byte("0x"+strings.Repeat("f", 64)), reply.TxHash)
	require.Contains(t, reply.GetErrorMessage(), "write transmission failed onchain")
	require.NotEmpty(t, mockForwarder.validatedFailedHashes)
	require.Contains(t, mockForwarder.validatedFailedHashes, "0x"+strings.Repeat("1", 64))
	require.Contains(t, mockForwarder.validatedFailedHashes, "0x"+strings.Repeat("f", 64))
}

func TestExecuteWriteReport_ReturnsErrorWhenFailedTxReceiptCannotBeValidated(t *testing.T) {
	transmissionID := newTestTransmissionID()
	rawReport := mustEncodedReportWithMetadata(t, transmissionID)

	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		WorkflowOwner:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WorkflowID:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	receiver := make([]byte, len(transmissionID.Receiver))
	copy(receiver, transmissionID.Receiver[:])

	sig := &sdk.AttributedSignature{Signature: []byte{1}, SignerId: 0}
	request := &aptoscap.WriteReportRequest{
		Receiver: receiver,
		Report: &sdk.ReportResponse{
			RawReport: rawReport,
			Sigs:      []*sdk.AttributedSignature{sig},
		},
		GasConfig: &aptoscap.GasConfig{MaxGasAmount: 1000},
	}

	maxGasLimiter, err := limits.MakeBoundLimiter(limits.Factory{}, settings.Uint64(1_000_000))
	require.NoError(t, err)
	reportSizeLimiter, err := limits.MakeBoundLimiter(limits.Factory{}, settings.Size(commoncfg.Byte*512))
	require.NoError(t, err)

	localHash := "0x" + strings.Repeat("a", 64)
	mockForwarder := &mockForwarderClient{
		pendingSender: transmissionID.Receiver,
		pendingHash:   localHash,
		failedHashErr: errors.New("failed tx receipt unavailable"),
	}

	a := &Aptos{
		forwarderClient:   mockForwarder,
		ConsensusHandler:  &passthroughAggregatableConsensusHandler{},
		lggr:              logger.Sugared(logger.Test(t)),
		maxGasAmountLimit: maxGasLimiter,
		reportSizeLimit:   reportSizeLimiter,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	reply, err := a.executeWriteReport(ctx, request, metadata)
	require.Error(t, err)
	require.Nil(t, reply)
	require.Contains(t, err.Error(), "failed hash resolution by receipt")
	require.NotEmpty(t, mockForwarder.validatedFailedHashes)
	for _, observedHash := range mockForwarder.validatedFailedHashes {
		require.Equal(t, localHash, observedHash)
	}
}

func TestResolveDeterministicFailedHash_AdvancesObserverRoundsUntilValidCandidate(t *testing.T) {
	transmissionID := newTestTransmissionID()
	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		ReferenceID:         "step1",
	}

	hashA := "0x" + strings.Repeat("a", 64)
	hashB := "0x" + strings.Repeat("b", 64)

	mockForwarder := &mockForwarderClient{
		validationErrByInput: map[string]error{
			hashA: errors.New("invalid hash"),
		},
		validatedHashByInput: map[string]string{
			hashB: hashB,
		},
	}

	a := &Aptos{
		forwarderClient: mockForwarder,
		ConsensusHandler: &scriptedAggregatableConsensusHandler{selectedByRequestID: map[string]*pb.Decimal{
			commonRequestID(metadata, 0): mustAptosHashDecimal(t, hashA),
			commonRequestID(metadata, 1): mustAptosHashDecimal(t, hashB),
		}},
		lggr: logger.Sugared(logger.Test(t)),
	}

	got, err := a.resolveDeterministicFailedHash(context.Background(), metadata, transmissionID, "", []byte("expected_raw_report"), 3)
	require.NoError(t, err)
	require.Equal(t, hashB, got)
	require.Contains(t, mockForwarder.validatedFailedHashes, hashB)
}

func TestResolveDeterministicFailedHash_UsesLocalCandidateWhenConsensusReturnsSameValue(t *testing.T) {
	transmissionID := newTestTransmissionID()
	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		ReferenceID:         "step1",
	}
	localHash := "0x" + strings.Repeat("c", 64)

	mockForwarder := &mockForwarderClient{
		validatedHashByInput: map[string]string{
			localHash: localHash,
		},
	}

	a := &Aptos{
		forwarderClient:  mockForwarder,
		ConsensusHandler: &passthroughAggregatableConsensusHandler{},
		lggr:             logger.Sugared(logger.Test(t)),
	}

	got, err := a.resolveDeterministicFailedHash(context.Background(), metadata, transmissionID, localHash, []byte("expected_raw_report"), 2)
	require.NoError(t, err)
	require.Equal(t, localHash, got)
	require.Empty(t, mockForwarder.failedHashLookupTransmitters)
}

func TestResolveDeterministicFailedHash_ReturnsErrorWhenRoundBudgetExhausted(t *testing.T) {
	transmissionID := newTestTransmissionID()
	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		ReferenceID:         "step1",
	}
	hashA := "0x" + strings.Repeat("a", 64)
	hashB := "0x" + strings.Repeat("b", 64)

	mockForwarder := &mockForwarderClient{
		validationErrByInput: map[string]error{
			hashA: errors.New("invalid hash A"),
			hashB: errors.New("invalid hash B"),
		},
	}

	a := &Aptos{
		forwarderClient: mockForwarder,
		ConsensusHandler: &scriptedAggregatableConsensusHandler{selectedByRequestID: map[string]*pb.Decimal{
			commonRequestID(metadata, 0): mustAptosHashDecimal(t, hashA),
			commonRequestID(metadata, 1): mustAptosHashDecimal(t, hashB),
		}},
		lggr: logger.Sugared(logger.Test(t)),
	}

	_, err := a.resolveDeterministicFailedHash(context.Background(), metadata, transmissionID, "", []byte("expected_raw_report"), 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to resolve deterministic failed hash after 2 consensus rounds")
}

type mockForwarderClient struct {
	pendingSender                     [32]byte
	pendingHash                       string
	failedHash                        string
	failedHashErr                     error
	failedHashByTransmitter           map[string]string
	failedHashErrByTransmitter        map[string]error
	validatedHashByInput              map[string]string
	validationErrByInput              map[string]error
	failedHashLookupTransmitters      []string
	failedHashLookupMaxLedgerVersions []uint64
	validatedFailedHashes             []string
}

func (m *mockForwarderClient) InvokeOnReport(_ context.Context, _ []byte, _ *sdk.ReportResponse, _ *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error) {
	hash := m.pendingHash
	if hash == "" {
		hash = "0xsubmitted"
	}
	return &aptostypes.SubmitTransactionReply{
		PendingTransaction: &aptostypes.PendingTransaction{
			Hash:   hash,
			Sender: m.pendingSender,
		},
	}, nil
}

func (m *mockForwarderClient) GetTransmissionInfo(_ context.Context, _ TransmissionID) (TransmissionInfo, error) {
	return TransmissionInfo{Success: false}, nil
}

func (m *mockForwarderClient) GetTransmissionTxHash(_ context.Context, _ TransmissionID, _ string, _ []byte) (string, error) {
	return "", nil
}

func (m *mockForwarderClient) ValidateFailedTxHash(_ context.Context, _ TransmissionID, txHash string, _ []byte) (string, error) {
	m.validatedFailedHashes = append(m.validatedFailedHashes, txHash)
	if err, ok := m.validationErrByInput[txHash]; ok {
		return "", err
	}
	if hash, ok := m.validatedHashByInput[txHash]; ok {
		return hash, nil
	}
	if m.failedHashErr != nil {
		return "", m.failedHashErr
	}
	return m.failedHash, nil
}

func (m *mockForwarderClient) GetTransmissionFailedTxHash(_ context.Context, _ TransmissionID, transmitters []string, maxLedgerVersion *uint64) (string, error) {
	if len(m.failedHashByTransmitter) == 0 && len(m.failedHashErrByTransmitter) == 0 {
		return "", errors.New("unexpected call to GetTransmissionFailedTxHash in this test")
	}
	if len(transmitters) != 1 {
		return "", fmt.Errorf("expected exactly one transmitter, got %d", len(transmitters))
	}
	if maxLedgerVersion != nil {
		m.failedHashLookupMaxLedgerVersions = append(m.failedHashLookupMaxLedgerVersions, *maxLedgerVersion)
	}
	transmitter := normalizeAptosHexAddress(transmitters[0])
	m.failedHashLookupTransmitters = append(m.failedHashLookupTransmitters, transmitter)
	if err, ok := m.failedHashErrByTransmitter[transmitter]; ok {
		return "", err
	}
	if hash, ok := m.failedHashByTransmitter[transmitter]; ok {
		return hash, nil
	}
	return "", fmt.Errorf("no failed hash for transmitter %s", transmitter)
}

type passthroughAggregatableConsensusHandler struct{}

func (h *passthroughAggregatableConsensusHandler) Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error) {
	ch := make(chan ctypes.Reply, 1)

	aggregatableReq, ok := request.(*ctypes.AggregatableRequest)
	if !ok {
		ch <- ctypes.Reply{Err: fmt.Errorf("unexpected request type %T", request)}
		return ch, nil
	}

	if err := aggregatableReq.CaptureObservation(ctx); err != nil {
		ch <- ctypes.Reply{Err: err}
		return ch, nil
	}

	observation, observationErr, ok := aggregatableReq.GetObservation()
	if !ok {
		ch <- ctypes.Reply{Err: fmt.Errorf("missing observation")}
		return ch, nil
	}
	if observationErr != nil {
		ch <- ctypes.Reply{Err: observationErr.Err()}
		return ch, nil
	}

	ch <- ctypes.Reply{Value: observation.Value}
	return ch, nil
}

type scriptedAggregatableConsensusHandler struct {
	selectedByRequestID map[string]*pb.Decimal
}

func (h *scriptedAggregatableConsensusHandler) Handle(_ context.Context, request ctypes.Request) (<-chan ctypes.Reply, error) {
	ch := make(chan ctypes.Reply, 1)
	aggregatableReq, ok := request.(*ctypes.AggregatableRequest)
	if !ok {
		ch <- ctypes.Reply{Err: fmt.Errorf("unexpected request type %T", request)}
		return ch, nil
	}

	if h.selectedByRequestID == nil {
		ch <- ctypes.Reply{Err: fmt.Errorf("no scripted consensus responses")}
		return ch, nil
	}
	value, ok := h.selectedByRequestID[aggregatableReq.ID()]
	if !ok {
		ch <- ctypes.Reply{Err: fmt.Errorf("missing scripted consensus response for request %s", aggregatableReq.ID())}
		return ch, nil
	}
	ch <- ctypes.Reply{Value: value}
	return ch, nil
}

func commonRequestID(metadata capabilities.RequestMetadata, round int) string {
	return fmt.Sprintf("%s:%s:aptos-failed-hash:round:%d", metadata.WorkflowExecutionID, metadata.ReferenceID, round)
}

func mustAptosHashDecimal(t *testing.T, hash string) *pb.Decimal {
	t.Helper()
	value, err := aptosHashToDecimal(hash)
	require.NoError(t, err)
	return value
}

func TestIsKnownForwarderAbortCode(t *testing.T) {
	require.True(t, isKnownForwarderAbortCode(6))      // direct module code
	require.True(t, isKnownForwarderAbortCode(65549))  // invalid_argument + 13
	require.True(t, isKnownForwarderAbortCode(327694)) // permission_denied + 14
	require.False(t, isKnownForwarderAbortCode(65570))
}

func TestIsForwarderAbortLocation(t *testing.T) {
	require.True(t, isForwarderAbortLocation("0xabc::platform::forwarder", ""))
	require.True(t, isForwarderAbortLocation("", "Move abort in 0xabc::platform_secondary::forwarder: 65549"))
	require.False(t, isForwarderAbortLocation("0xabc::customer_module", "Move abort in 0xabc::customer_module: 1"))
}
