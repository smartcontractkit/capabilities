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

type mockForwarderClient struct {
	pendingSender         [32]byte
	pendingHash           string
	failedHash            string
	failedHashErr         error
	validatedFailedHashes []string
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
	if m.failedHashErr != nil {
		return "", m.failedHashErr
	}
	return m.failedHash, nil
}

func (m *mockForwarderClient) GetTransmissionFailedTxHash(_ context.Context, _ TransmissionID, transmitters []string) (string, error) {
	_ = transmitters
	return "", errors.New("unexpected call to GetTransmissionFailedTxHash in this test")
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
