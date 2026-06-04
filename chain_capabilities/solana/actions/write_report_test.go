package actions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

	p2ptypes "github.com/smartcontractkit/libocr/ragep2p/types"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
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
				ReportContext: RandomBytes(reportContextLen),
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "metadata: raw too short, want ≥109, got 0")
	})
	t.Run("Too many signatures", func(t *testing.T) {
		sigs := make([]*workflowpb.AttributedSignature, maxOracles+1)
		for i := range sigs {
			sigs[i] = &workflowpb.AttributedSignature{Signature: RandomBytes(signatureLen)}
		}
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				ReportContext: RandomBytes(reportContextLen),
				Sigs:          sigs,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf("too many signatures: got %d, max %d", maxOracles+1, maxOracles))
	})
	t.Run("Invalid signature length", func(t *testing.T) {
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				ReportContext: RandomBytes(reportContextLen),
				Sigs: []*workflowpb.AttributedSignature{
					{Signature: RandomBytes(32)},
				},
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf("signature 0 has invalid length: got 32, want %d", signatureLen))
	})
	t.Run("Invalid report context length", func(t *testing.T) {
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				ReportContext: []byte{1, 2, 3},
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf("report context has invalid length: got 3, want %d", reportContextLen))
	})
	t.Run("Report signatures are not empty", func(t *testing.T) {
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				ReportContext: RandomBytes(reportContextLen),
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
				ReportContext: RandomBytes(reportContextLen),
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
				ReportContext: RandomBytes(reportContextLen),
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
				ReportContext: RandomBytes(reportContextLen),
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
				ReportContext: RandomBytes(reportContextLen),
				Sigs:          generateRandomSignatures(),
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "workflowExecutionID in the report does not match WorkflowExecutionID in the request metadata.")
	})
	t.Run("Short workflow name should pass validation when report and request match", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		// 4 hex chars — shorter than the full 20-char workflow name field; Encode pads
		// the copy during serialization and Decode restores the 20-char form for comparison.
		reportMetadata.WorkflowName = "aabb"
		encodedReportMetadata, encErr := reportMetadata.Encode()
		require.NoError(t, encErr)
		// Request and report use the same unpadded WorkflowName on metadata; validation pads
		// the request name the same way as metadata encoding (ASCII "0" to 20 chars).
		err := helper.solana.validateInputsAndReportMetadata(
			createTestRequestMetadata(reportMetadata),
			&solcap.WriteReportRequest{
				Receiver: key.PublicKey().Bytes(),
				Report: &workflowpb.ReportResponse{
					RawReport:     encodedReportMetadata,
					ReportContext: RandomBytes(reportContextLen),
					Sigs:          generateRandomSignatures(),
				},
			},
		)
		require.NoError(t, err)
	})
	t.Run("Invalid remaining account public key length", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		req := createTestWriteReportReq(reportMetadata)
		req.RemainingAccounts = []*solcap.AccountMeta{
			{PublicKey: []byte{1, 2, 3}, IsWritable: false},
		}
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "remaining account 0")
		require.Contains(t, err.Error(), "32 bytes")
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("Nil remaining account meta", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		req := createTestWriteReportReq(reportMetadata)
		req.RemainingAccounts = []*solcap.AccountMeta{nil}
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "remaining account 0: nil account meta")
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{}, errors.New(expectedError))
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), &solcap.WriteReportRequest{
			Receiver: key.PublicKey().Bytes(),
			Report: &workflowpb.ReportResponse{
				RawReport:     encodedReportMetadata,
				ReportContext: RandomBytes(reportContextLen),
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

		transmissionInfo := TransmissionInfo{
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
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &solcap.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
			State: TransmissionStateNotAttempted,
		}, nil).Once()

		helper.creForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, mock.Anything, signedReport, mock.Anything).Return(&soltypes.SubmitTransactionReply{
			Signature: soltypes.Signature(sig),
		}, nil)

		transmissionInfo := TransmissionInfo{
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
			ReportContext: RandomBytes(reportContextLen),
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
	return []*workflowpb.AttributedSignature{
		{Signature: RandomBytes(signatureLen)},
		{Signature: RandomBytes(signatureLen)},
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
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &solcap.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
			State: TransmissionStateNotAttempted,
		}, nil).Once()

		helper.creForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, mock.Anything, signedReport, mock.Anything).Return(&soltypes.SubmitTransactionReply{
			Signature: soltypes.Signature(sig),
		}, nil)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
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
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &solcap.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
			State: TransmissionStateNotAttempted,
		}, nil).Once()

		helper.creForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, mock.Anything, signedReport, mock.Anything).Return(&soltypes.SubmitTransactionReply{
			Signature: soltypes.Signature(sig),
		}, nil)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
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
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          generateRandomSignatures(),
		}
		writeReportRequest := &solcap.WriteReportRequest{
			Receiver: receiverAddress.Bytes(),
			Report:   signedReport,
		}
		capabilitiesMetadata := createTestRequestMetadata(reportMetadata)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
			State: TransmissionStateNotAttempted,
		}, nil).Once()

		helper.creForwarderClient.On("InvokeOnReport", mock.Anything, receiverAddress, mock.Anything, signedReport, mock.Anything).Return(&soltypes.SubmitTransactionReply{
			Signature: soltypes.Signature(sig),
		}, nil)

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
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

		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{
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

func TestValidateRemainingAccountHash(t *testing.T) {
	t.Parallel()

	// buildRawReport mirrors the on-chain report layout:
	//   [109 bytes OCR3 metadata][32 bytes account_hash][4 bytes LE payload_len][payload...]
	// This is how the keystone-forwarder deserializes ForwarderReport from rawReport[METADATA_LENGTH..].
	buildRawReport := func(t *testing.T, metadata ocrtypes.Metadata, accountHash [32]byte, payload []byte) []byte {
		t.Helper()
		header, err := metadata.Encode()
		require.NoError(t, err)
		require.Len(t, header, ocrtypes.MetadataLen)

		// Borsh-encode ForwarderReport: fixed [u8;32] hash + Vec<u8> payload (4-byte LE length prefix)
		payloadLen := make([]byte, 4)
		payloadLen[0] = byte(len(payload))
		payloadLen[1] = byte(len(payload) >> 8)
		payloadLen[2] = byte(len(payload) >> 16)
		payloadLen[3] = byte(len(payload) >> 24)

		raw := make([]byte, 0, len(header)+32+4+len(payload))
		raw = append(raw, header...)
		raw = append(raw, accountHash[:]...)
		raw = append(raw, payloadLen...)
		raw = append(raw, payload...)
		return raw
	}

	// computeAccountHash mirrors calculateHash from the workflow WASM binary and the
	// on-chain forwarder: SHA-256 over concatenated 32-byte public keys.
	computeAccountHash := func(accounts []*solcap.AccountMeta) [32]byte {
		var buf []byte
		for _, acc := range accounts {
			buf = append(buf, acc.GetPublicKey()...)
		}
		return sha256.Sum256(buf)
	}

	makeAccounts := func(n int) []*solcap.AccountMeta {
		accs := make([]*solcap.AccountMeta, n)
		for i := range accs {
			accs[i] = &solcap.AccountMeta{PublicKey: RandomBytes(32)}
		}
		return accs
	}

	t.Run("Valid hash with multiple remaining accounts", func(t *testing.T) {
		accounts := makeAccounts(8)
		hash := computeAccountHash(accounts)
		rawReport := buildRawReport(t, createTestReportMetadata(), hash, []byte("some payload"))

		err := validateRemainingAccountsHash(accounts, rawReport)
		require.NoError(t, err)
	})

	t.Run("Valid hash with single remaining account", func(t *testing.T) {
		accounts := makeAccounts(1)
		hash := computeAccountHash(accounts)
		rawReport := buildRawReport(t, createTestReportMetadata(), hash, nil)

		err := validateRemainingAccountsHash(accounts, rawReport)
		require.NoError(t, err)
	})

	t.Run("No remaining accounts skips validation", func(t *testing.T) {
		err := validateRemainingAccountsHash(nil, []byte("short"))
		require.NoError(t, err)
	})

	t.Run("Mismatch when accounts differ from report hash", func(t *testing.T) {
		accounts := makeAccounts(4)
		hash := computeAccountHash(accounts)
		rawReport := buildRawReport(t, createTestReportMetadata(), hash, []byte("payload"))

		differentAccounts := makeAccounts(4)
		err := validateRemainingAccountsHash(differentAccounts, rawReport)
		require.Error(t, err)
		require.Contains(t, err.Error(), "remaining account hash mismatch")
	})

	t.Run("Mismatch when account order changes", func(t *testing.T) {
		accounts := makeAccounts(3)
		hash := computeAccountHash(accounts)
		rawReport := buildRawReport(t, createTestReportMetadata(), hash, []byte("payload"))

		reordered := []*solcap.AccountMeta{accounts[2], accounts[0], accounts[1]}
		err := validateRemainingAccountsHash(reordered, rawReport)
		require.Error(t, err)
		require.Contains(t, err.Error(), "remaining account hash mismatch")
	})

	t.Run("Report too short", func(t *testing.T) {
		accounts := makeAccounts(1)
		shortReport := make([]byte, ocrtypes.MetadataLen+10) // not enough for 32-byte hash
		err := validateRemainingAccountsHash(accounts, shortReport)
		require.Error(t, err)
		require.Contains(t, err.Error(), "raw report too short to contain account hash")
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

func TestToPayload(t *testing.T) {
	t.Parallel()

	t.Run("Valid payload", func(t *testing.T) {
		report := &workflowpb.ReportResponse{
			RawReport:     RandomBytes(120),
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          generateRandomSignatures(),
		}
		payload, err := toPayload(report)
		require.NoError(t, err)
		expectedLen := 1 + len(report.Sigs)*signatureLen + len(report.RawReport) + reportContextLen
		require.Len(t, payload, expectedLen)
		require.Equal(t, byte(len(report.Sigs)), payload[0])
	})

	t.Run("Too many signatures", func(t *testing.T) {
		sigs := make([]*workflowpb.AttributedSignature, maxOracles+1)
		for i := range sigs {
			sigs[i] = &workflowpb.AttributedSignature{Signature: RandomBytes(signatureLen)}
		}
		_, err := toPayload(&workflowpb.ReportResponse{
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          sigs,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "signature count")
	})

	t.Run("Invalid signature length", func(t *testing.T) {
		_, err := toPayload(&workflowpb.ReportResponse{
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          []*workflowpb.AttributedSignature{{Signature: RandomBytes(32)}},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "signature 0 length 32")
	})

	t.Run("Invalid report context length", func(t *testing.T) {
		_, err := toPayload(&workflowpb.ReportResponse{
			ReportContext: RandomBytes(10),
			Sigs:          generateRandomSignatures(),
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "report context length 10")
	})
}
