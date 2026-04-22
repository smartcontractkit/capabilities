package actions

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/test-go/testify/mock"
	"github.com/test-go/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	workflowpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"
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

		result, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), createTestWriteReportReq(reportMetadata))
		require.NoError(t, err)
		require.Empty(t, result.ResponseMetadata.Metering)
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

		txFeeInLamports := uint64(5000)
		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: txFeeInLamports},
		}, nil)

		result, err := helper.solana.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		validateMeteringWriteReport(t, result.ResponseMetadata, 1, "0.000005")
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
		messageBuilder:           monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
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

func validateMeteringWriteReport(t *testing.T, metadata capabilities.ResponseMetadata, chainSelector int, expectedValue string) {
	t.Helper()
	require.Len(t, metadata.Metering, 1)
	meteringNodeDetail := metadata.Metering[0]
	require.Equal(t, fmt.Sprintf(metering.WriteReportSpendUnitFormat, chainSelector), meteringNodeDetail.SpendUnit)
	require.Equal(t, expectedValue, meteringNodeDetail.SpendValue)
	require.Empty(t, meteringNodeDetail.Peer2PeerID, "Peer2PeerID should be empty as it will be assigned by the engine")
}

func TestWriteReport_MeteringMetadata(t *testing.T) {
	t.Parallel()

	key, _ := solana.NewRandomPrivateKey()
	sig := solana.Signature{1, 2, 3}

	t.Run("Successful first transmission includes metering metadata", func(t *testing.T) {
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

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(&TransmissionInfo{
			State: TransmissionStateSucceeded,
		}, nil).Once()

		txFeeInLamports := uint64(10000)
		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: txFeeInLamports},
		}, nil)

		result, err := helper.solana.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		require.NotNil(t, result)

		validateMeteringWriteReport(t, result.ResponseMetadata, 1, "0.00001")
	})

	t.Run("Failed transmission still includes metering metadata", func(t *testing.T) {
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

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(&TransmissionInfo{
			State:     TransmissionStateFailed,
			Signature: sig,
		}, nil).Once()

		txFeeInLamports := uint64(5000)
		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: txFeeInLamports},
		}, nil)

		result, err := helper.solana.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, solcap.TxStatus_TX_STATUS_ABORTED, result.Response.TxStatus)

		validateMeteringWriteReport(t, result.ResponseMetadata, 1, "0.000005")
	})

	t.Run("Fee calculation failure does not fail WriteReport", func(t *testing.T) {
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

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(&TransmissionInfo{
			State: TransmissionStateSucceeded,
		}, nil).Once()

		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(
			(*soltypes.GetTransactionReply)(nil), errors.New("rpc error: transaction not found"))

		result, err := helper.solana.WriteReport(ctx, capabilitiesMetadata, writeReportRequest)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, solcap.TxStatus_TX_STATUS_SUCCESS, result.Response.TxStatus)

		require.Empty(t, result.ResponseMetadata.Metering)
	})

	t.Run("Pre-existing successful transmission has no metering", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		helper := createMocksAndCapability(t, testLogger)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(&TransmissionInfo{
			State: TransmissionStateSucceeded,
		}, nil)

		reportMetadata := createTestReportMetadata()

		result, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), createTestWriteReportReq(reportMetadata))
		require.NoError(t, err)
		require.NotNil(t, result)

		require.Empty(t, result.ResponseMetadata.Metering)
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func setupSolanaPollTransmissionInfo(t *testing.T) (*WriteReport, *TransmissionInfoProvider_mock) {
	t.Helper()
	testLogger := logger.Test(t)
	mockTrInfo := NewTransmissionInfoProvider_mock(t)

	var peer0, peer1, peer2, peer3 p2ptypes.PeerID
	peer0[0], peer1[0], peer2[0], peer3[0] = 0x01, 0x02, 0x03, 0x04
	scheduler := ts.NewTransmissionScheduler(
		peer0,
		[]p2ptypes.PeerID{peer0, peer1, peer2, peer3},
		10*time.Millisecond,
		2,
		testLogger,
	)

	wr := &WriteReport{
		transmissionInfoProvider: mockTrInfo,
		lggr:                     testLogger,
		beholderProcessor:        NopBeholderProcessor{},
		messageBuilder:           monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		transmissionScheduler:    scheduler,
	}
	return wr, mockTrInfo
}

func TestPollTransmissionInfo_RaceConditions_Solana(t *testing.T) {
	t.Parallel()

	t.Run("timer returns fresh state via final boundary read", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		wr, mockTrInfo := setupSolanaPollTransmissionInfo(t)
		wr.transmissionScheduler.DeltaStage = 150 * time.Millisecond

		var chainStateUpdated atomic.Bool
		go func() {
			time.Sleep(120 * time.Millisecond)
			chainStateUpdated.Store(true)
		}()

		mockTrInfo.EXPECT().
			GetTransmissionInfo(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, [32]byte) (TransmissionInfo, error) {
				if chainStateUpdated.Load() {
					return TransmissionInfo{State: TransmissionStateSucceeded}, nil
				}
				return TransmissionInfo{State: TransmissionStateNotAttempted}, nil
			}).
			Maybe()

		var transmissionID [32]byte
		info, err := wr.pollTransmissionInfo(ctx, transmissionID, 1)
		require.NoError(t, err)
		require.True(t, chainStateUpdated.Load(), "chain state should have updated before stage timer returned")
		require.Equal(t, TransmissionStateSucceeded, info.State)
	})

	t.Run("all rpc errors including boundary read return error", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()

		wr, mockTrInfo := setupSolanaPollTransmissionInfo(t)
		wr.transmissionScheduler.DeltaStage = 50 * time.Millisecond

		var rpcCalls atomic.Int64
		mockTrInfo.EXPECT().
			GetTransmissionInfo(mock.Anything, mock.Anything).
			RunAndReturn(func(context.Context, [32]byte) (TransmissionInfo, error) {
				rpcCalls.Add(1)
				return TransmissionInfo{}, errors.New("rpc unavailable")
			}).
			Maybe()

		var transmissionID [32]byte
		_, err := wr.pollTransmissionInfo(ctx, transmissionID, 2)
		require.Greater(t, rpcCalls.Load(), int64(0))
		require.Error(t, err)
	})
}

func TestGetFee(t *testing.T) {
	t.Parallel()

	t.Run("Converts lamports to SOL correctly", func(t *testing.T) {
		testLogger := logger.Test(t)
		mockSolanaService := mocks.NewSolanaService(t)

		wr := &WriteReport{
			SolanaService: mockSolanaService,
			lggr:          testLogger,
		}

		sig := solana.Signature{1, 2, 3}
		txFeeInLamports := uint64(5000)
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: txFeeInLamports},
		}, nil)

		fee, err := wr.getFee(t.Context(), sig)
		require.NoError(t, err)
		require.Equal(t, "0.000005", fee.Text('f', -1))
	})

	t.Run("Handles large fee values", func(t *testing.T) {
		testLogger := logger.Test(t)
		mockSolanaService := mocks.NewSolanaService(t)

		wr := &WriteReport{
			SolanaService: mockSolanaService,
			lggr:          testLogger,
		}

		sig := solana.Signature{4, 5, 6}
		txFeeInLamports := uint64(1_000_000_000) // 1 SOL
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: txFeeInLamports},
		}, nil)

		fee, err := wr.getFee(t.Context(), sig)
		require.NoError(t, err)
		require.Equal(t, "1", fee.Text('f', -1))
	})

	t.Run("Returns error when GetTransaction fails", func(t *testing.T) {
		testLogger := logger.Test(t)
		mockSolanaService := mocks.NewSolanaService(t)

		wr := &WriteReport{
			SolanaService: mockSolanaService,
			lggr:          testLogger,
		}

		sig := solana.Signature{7, 8, 9}
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(
			(*soltypes.GetTransactionReply)(nil), errors.New("rpc error"))

		_, err := wr.getFee(t.Context(), sig)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transaction")
	})
}
