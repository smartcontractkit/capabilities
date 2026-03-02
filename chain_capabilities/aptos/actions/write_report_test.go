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
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	"github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestExecuteWriteReport_ReturnsDeterministicFailedHashOnInvalidCall(t *testing.T) {
	transmissionID := newTestTransmissionID()
	rawReport := mustEncodedReportWithMetadata(t, transmissionID)

	transmitters := []string{"0xaaa", "0xbbb"}
	orderedTransmitters := canonicalTransmitterOrderForTransmission(transmitters, transmissionID)
	invalidHash := "0x" + strings.Repeat("d", 64)
	validHash := "0x" + strings.Repeat("f", 64)

	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		WorkflowOwner:       "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		WorkflowID:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ReferenceID:         "step1",
		WorkflowDonID:       17,
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

	localHash := "0x" + strings.Repeat("1", 64)
	mockForwarder := &mockForwarderClient{
		pendingSender: transmissionID.Receiver,
		pendingHash:   localHash,
		failedHashByTransmitter: map[string]string{
			orderedTransmitters[0]: invalidHash,
			orderedTransmitters[1]: validHash,
		},
		validationErrByInput: map[string]error{
			invalidHash: errors.New("invalid hash"),
		},
		validatedHashByInput: map[string]string{
			localHash: localHash,
			validHash: validHash,
		},
	}

	aptosService := typesmocks.NewAptosService(t)
	aptosService.On("LedgerVersion", mock.Anything).Return(uint64(250), nil).Once()

	registry := &mockCapabilitiesRegistry{
		config: capabilityConfigWithTransmitters(t, transmitters),
	}

	a := &Aptos{
		aptosService:       aptosService,
		forwarderClient:    mockForwarder,
		ConsensusHandler:   &passthroughAggregatableConsensusHandler{},
		capabilityRegistry: registry,
		capabilityID:       "aptos-capability-id",
		lggr:               logger.Sugared(logger.Test(t)),
		maxGasAmountLimit:  maxGasLimiter,
		reportSizeLimit:    reportSizeLimiter,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	reply, err := a.executeWriteReport(ctx, request, metadata)
	require.NoError(t, err)
	require.NotNil(t, reply)
	require.Equal(t, aptoscap.TxStatus_TX_STATUS_FAILED, reply.TxStatus)
	require.Equal(t, []byte(validHash), reply.TxHash)
	require.Contains(t, reply.GetErrorMessage(), "write transmission failed onchain")
	require.NotEmpty(t, mockForwarder.validatedFailedHashes)
	require.Contains(t, mockForwarder.validatedFailedHashes, localHash)
	require.Contains(t, mockForwarder.validatedFailedHashes, invalidHash)
	require.Contains(t, mockForwarder.validatedFailedHashes, validHash)
	require.Equal(t, "aptos-capability-id", registry.capabilityID)
	require.Equal(t, uint32(17), registry.donID)
	require.NotEmpty(t, mockForwarder.failedHashLookupTransmitters)
	for _, cutoff := range mockForwarder.failedHashLookupMaxLedgerVersions {
		require.Equal(t, uint64(248), cutoff)
	}
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

func TestResolveDeterministicFailedHash_UsesRegistryTransmittersAndCutoff(t *testing.T) {
	transmissionID := newTestTransmissionID()
	transmitters := []string{"0xaaa", "0xbbb", "0xccc"}
	orderedTransmitters := canonicalTransmitterOrderForTransmission(transmitters, transmissionID)

	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		ReferenceID:         "step1",
		WorkflowDonID:       42,
	}

	invalidHash := "0x" + strings.Repeat("a", 64)
	validHash := "0x" + strings.Repeat("b", 64)

	mockForwarder := &mockForwarderClient{
		failedHashByTransmitter: map[string]string{
			orderedTransmitters[0]: invalidHash,
			orderedTransmitters[1]: validHash,
		},
		validationErrByInput: map[string]error{
			invalidHash: errors.New("invalid hash"),
		},
		validatedHashByInput: map[string]string{
			validHash: validHash,
		},
	}

	aptosService := typesmocks.NewAptosService(t)
	aptosService.On("LedgerVersion", mock.Anything).Return(uint64(500), nil).Once()

	registry := &mockCapabilitiesRegistry{
		config: capabilityConfigWithTransmitters(t, transmitters),
	}

	a := &Aptos{
		aptosService:       aptosService,
		forwarderClient:    mockForwarder,
		ConsensusHandler:   &passthroughAggregatableConsensusHandler{},
		capabilityRegistry: registry,
		capabilityID:       "aptos-capability-id",
		lggr:               logger.Sugared(logger.Test(t)),
	}

	got, err := a.resolveDeterministicFailedHash(context.Background(), metadata, transmissionID, "", []byte("expected_raw_report"))
	require.NoError(t, err)
	require.Equal(t, validHash, got)
	require.Equal(t, "aptos-capability-id", registry.capabilityID)
	require.Equal(t, uint32(42), registry.donID)
	require.GreaterOrEqual(t, len(mockForwarder.failedHashLookupTransmitters), 2)
	require.Equal(t, orderedTransmitters[0], mockForwarder.failedHashLookupTransmitters[0])
	require.Equal(t, orderedTransmitters[1], mockForwarder.failedHashLookupTransmitters[1])
	require.NotEmpty(t, mockForwarder.failedHashLookupMaxLedgerVersions)
	for _, cutoff := range mockForwarder.failedHashLookupMaxLedgerVersions {
		require.Equal(t, uint64(498), cutoff)
	}
}

func TestResolveDeterministicFailedHash_ReturnsErrorWhenNoValidatedRegistryCandidate(t *testing.T) {
	transmissionID := newTestTransmissionID()
	transmitters := []string{"0xaaa", "0xbbb"}
	orderedTransmitters := canonicalTransmitterOrderForTransmission(transmitters, transmissionID)

	metadata := capabilities.RequestMetadata{
		WorkflowExecutionID: hex.EncodeToString(transmissionID.WorkflowExecutionID[:]),
		ReferenceID:         "step1",
		WorkflowDonID:       42,
	}

	hashA := "0x" + strings.Repeat("a", 64)
	hashB := "0x" + strings.Repeat("b", 64)

	mockForwarder := &mockForwarderClient{
		failedHashByTransmitter: map[string]string{
			orderedTransmitters[0]: hashA,
			orderedTransmitters[1]: hashB,
		},
		validationErrByInput: map[string]error{
			hashA: errors.New("invalid hash A"),
			hashB: errors.New("invalid hash B"),
		},
	}

	aptosService := typesmocks.NewAptosService(t)
	aptosService.On("LedgerVersion", mock.Anything).Return(uint64(100), nil).Once()

	a := &Aptos{
		aptosService:       aptosService,
		forwarderClient:    mockForwarder,
		ConsensusHandler:   &passthroughAggregatableConsensusHandler{},
		capabilityRegistry: &mockCapabilitiesRegistry{config: capabilityConfigWithTransmitters(t, transmitters)},
		capabilityID:       "aptos-capability-id",
		lggr:               logger.Sugared(logger.Test(t)),
	}

	_, err := a.resolveDeterministicFailedHash(context.Background(), metadata, transmissionID, "", []byte("expected_raw_report"))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no validated failed tx hash found from registry transmitters")
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

type mockCapabilitiesRegistry struct {
	core.UnimplementedCapabilitiesRegistry
	config       capabilities.CapabilityConfiguration
	err          error
	capabilityID string
	donID        uint32
}

func (m *mockCapabilitiesRegistry) ConfigForCapability(_ context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	m.capabilityID = capabilityID
	m.donID = donID
	if m.err != nil {
		return capabilities.CapabilityConfiguration{}, m.err
	}
	return m.config, nil
}

func capabilityConfigWithTransmitters(t *testing.T, transmitters []string) capabilities.CapabilityConfiguration {
	t.Helper()

	transmittersValue, err := values.Wrap(transmitters)
	require.NoError(t, err)

	specConfig := values.EmptyMap()
	specConfig.Underlying[aptosSpecConfigTransmittersListKey] = transmittersValue

	return capabilities.CapabilityConfiguration{
		SpecConfig: specConfig,
	}
}
