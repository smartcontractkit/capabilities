package actions

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

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
		failedHash:    "0xfailed-canonical",
	}

	a := &Aptos{
		forwarderClient:   mockForwarder,
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
	require.Equal(t, []byte("0xfailed-canonical"), reply.TxHash)
	require.Contains(t, reply.GetErrorMessage(), "write transmission did not succeed before timeout")
	require.Equal(t, []string{normalizeAptosHexAddress(transmissionID.Receiver.StringLong())}, mockForwarder.failedHashTransmitters)
}

type mockForwarderClient struct {
	pendingSender          [32]byte
	failedHash             string
	failedHashTransmitters []string
}

func (m *mockForwarderClient) InvokeOnReport(_ context.Context, _ []byte, _ *sdk.ReportResponse, _ *aptoscap.GasConfig) (*aptostypes.SubmitTransactionReply, error) {
	return &aptostypes.SubmitTransactionReply{
		PendingTransaction: &aptostypes.PendingTransaction{
			Hash:   "0xsubmitted",
			Sender: m.pendingSender,
		},
	}, nil
}

func (m *mockForwarderClient) GetTransmissionInfo(_ context.Context, _ TransmissionID) (TransmissionInfo, error) {
	return TransmissionInfo{Success: false}, nil
}

func (m *mockForwarderClient) GetTransmissionTxHash(_ context.Context, _ TransmissionID, _ string) (string, error) {
	return "", nil
}

func (m *mockForwarderClient) GetTransmissionFailedTxHash(_ context.Context, _ TransmissionID, transmitters []string) (string, error) {
	m.failedHashTransmitters = append([]string{}, transmitters...)
	return m.failedHash, nil
}
