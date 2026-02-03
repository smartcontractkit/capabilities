package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	"github.com/test-go/testify/mock"
	"github.com/test-go/testify/require"
	"google.golang.org/protobuf/proto"
)

type testHelper struct {
	solanaService            *mocks.SolanaService
	transmissionInfoProvider *TransmissionInfoProvider_mock
	creForwarderClient       *CREForwarderClient_mock
	solana                   *Solana
}

func TestWriteReport_InputValidation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	lggr := logger.Test(t)
	key, _ := solana.NewRandomPrivateKey()
	helper := createMocksAndCapability(t, lggr)

	t.Run("Invalid receiver address", func(t *testing.T) {
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Report: &workflowpb.ReportResponse{},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "received public key is not 32 bytes long. key in hex: ")
	})
	t.Run("Invalid report metadata", func(t *testing.T) {
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				Sigs: generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "metadata: raw too short, want ≥109, got 0")
	})
	t.Run("Report signatures are not empty", func(t *testing.T) {
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				ReportContext: []byte{},
				Sigs:          []*workflowpb.AttributedSignature{},
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "no signatures provided")
	})
	t.Run("Invalid request metadata", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		reportMetadata.Version = 20
		encodedReportMetadata, _ := reportMetadata.Encode()
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported report version: 20")
	})
	t.Run("Workflow names do not match", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		workflowName := [10]byte(RandomBytes(10))
		reportMetadata.WorkflowName = hex.EncodeToString(workflowName[:])
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowName in the report does not match WorkflowName in the request metadata.")
	})
	t.Run("Workflow IDs do not match", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		workflowID := [32]byte(RandomBytes(32))
		reportMetadata.WorkflowID = hex.EncodeToString(workflowID[:])
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowID in the report does not match WorkflowID in the request metadata.")
	})
	t.Run("Workflow execution IDs do not match and workflow name less than 10 characters work", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		reportMetadata.WorkflowName = "12345"
		encodedReportMetadata, _ := reportMetadata.Encode()
		workflowID := [32]byte(RandomBytes(32))
		reportMetadata.ExecutionID = hex.EncodeToString(workflowID[:])
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata.")
	})
}
func TestWriteReport_ExecuteWriteReport(t *testing.T) {
	t.Parallel()

	key, _ := solana.NewRandomPrivateKey()
	sig := solana.Signature{1, 2, 3}
	t.Run("Fail with invalid workflow execution id", func(t *testing.T) {
		ctx := t.Context()
		lggr := logger.Test(t)
		helper := createMocksAndCapability(t, lggr)
		expectedError := "some error"
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(&TransmissionInfo{}, errors.New(expectedError))
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: []byte{},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), expectedError)
	})
	t.Run("TX already transmitted successfully", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		helper := createMocksAndCapability(t, testLogger)

		transmissionInfo := &TransmissionInfo{
			State: TransmissionStateSucceeded,
		}
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil)

		reportMetadata := createTestReportMetadata()

		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), createTestWriteReportReq(reportMetadata))
		require.NoError(t, err)
	})
	t.Run("TX first transmission - Successful TX execution", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		helper := createMocksAndCapability(t, testLogger)

		receiverAddress := key.PublicKey()
		reportMetadata := createTestReportMetadata()
		encodedReportMetadata, _ := reportMetadata.Encode()

		signedReport := &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &solcap.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(&TransmissionInfo{
			State: TransmissionStateNotAttempted,
		}, nil).Once()

		helper.creForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, mock.Anything, signedReport, mock.Anything).Return(&soltypes.SubmitTransactionReply{
			Signature: soltypes.Signature(sig),
		}, nil)

		transmissionInfo := &TransmissionInfo{
			State: TransmissionStateSucceeded,
		}
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(transmissionInfo, nil).Once()

		_, err := helper.solana.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
	})

}
func createTestWriteReportReq(metadata ocrtypes.Metadata) *solcap.WriteReportRequest {
	encodedReportMetadata, _ := metadata.Encode()

	key, _ := solana.NewRandomPrivateKey()
	return &solcap.WriteReportRequest{
		Receiver: key.PublicKey().Bytes(),
		Report: &workflowpb.ReportResponse{
			RawReport:     encodedReportMetadata,
			ReportContext: []byte{},
			Sigs:          generateRandomSignatures(),
		},
	}
}
func createMocksAndCapability(t *testing.T, lggr logger.Logger) *testHelper {
	mockSolanaService := mocks.NewSolanaService(t)
	mockTrInfo := NewTransmissionInfoProvider_mock(t)
	mockClient := NewCREForwarderClient_mock(t)
	service := &Solana{
		SolanaService:            mockSolanaService,
		forwarderClient:          mockClient,
		transmissionInfoProvider: mockTrInfo,
		beholderProcessor:        NopBeholderProcessor{},
		messageBuilder:           &monitoring.MessageBuilder{},
		chainSelector:            1,
		lggr:                     logger.Sugared(lggr),
	}
	require.NoError(t, service.initLimiters(limits.Factory{Logger: lggr}))
	require.NotNil(t, service.txComputeLimit)
	return &testHelper{mockSolanaService, mockTrInfo, mockClient, service}
}

type NopBeholderProcessor struct{}

func (NopBeholderProcessor) Process(_ context.Context, _ proto.Message, _ ...any) error { return nil }
func generateRandomSignatures() []*workflowpb.AttributedSignature {
	sig := [32]byte{1, 2, 3}
	return []*workflowpb.AttributedSignature{
		{Signature: sig[:]},
		{Signature: sig[:]},
	}
}
func createTestReportMetadata() ocrtypes.Metadata {
	return ocrtypes.Metadata{
		Version:          1,
		ExecutionID:      hex.EncodeToString(RandomBytes(32)),
		Timestamp:        1000,
		DONID:            10,
		DONConfigVersion: 2,
		WorkflowID:       hex.EncodeToString(RandomBytes(32)),
		WorkflowName:     hex.EncodeToString(RandomBytes(10)),
		WorkflowOwner:    hex.EncodeToString(RandomBytes(20)),
		ReportID:         hex.EncodeToString(RandomBytes(2)),
	}
}

func createTestRequestMetadata(metadata ocrtypes.Metadata) capabilities.RequestMetadata {
	return capabilities.RequestMetadata{
		WorkflowID:               metadata.WorkflowID,
		WorkflowOwner:            metadata.WorkflowOwner,
		WorkflowName:             metadata.WorkflowName,
		WorkflowDonID:            metadata.DONID,
		WorkflowDonConfigVersion: metadata.DONConfigVersion,
		WorkflowExecutionID:      metadata.ExecutionID,
	}
}

func RandomBytes(n int) []byte {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		panic(err)
	}
	return b
}
