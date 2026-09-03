package actions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	ocrtypes "github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/types"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
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
	forwarderState           solana.PublicKey
}

// expectReceiverIsProgram mocks the receiver account lookup that input validation performs,
// returning an existing, executable account.
func (h *testHelper) expectReceiverIsProgram(receiver solana.PublicKey) {
	h.solanaService.On("GetAccountInfoWithOpts", mock.Anything, mock.MatchedBy(func(req soltypes.GetAccountInfoRequest) bool {
		return req.Account == soltypes.PublicKey(receiver)
	})).Return(&soltypes.GetAccountInfoReply{Value: &soltypes.Account{Executable: true}}, nil)
}

// validWriteReportReq builds a request that passes every input check and mocks the
// receiver lookup accordingly.
func (h *testHelper) validWriteReportReq(t *testing.T, metadata ocrtypes.Metadata) *solcap.WriteReportRequest {
	t.Helper()
	key, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	h.expectReceiverIsProgram(key.PublicKey())
	return buildWriteReportReq(t, h.forwarderState, metadata, key.PublicKey())
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
	t.Run("Zero receiver address", func(t *testing.T) {
		_, err := helper.solana.WriteReport(ctx, capabilities.RequestMetadata{WorkflowID: "wf-id"}, &solcap.WriteReportRequest{
			Receiver: solana.PublicKey{}.Bytes(),
			Report:   &workflowpb.ReportResponse{},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "receiver public key is empty")
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
		req := helper.validWriteReportReq(t, reportMetadata)
		require.Equal(t, encodedReportMetadata, req.Report.RawReport[:ocrtypes.MetadataLen])
		err := helper.solana.validateInputsAndReportMetadata(
			ctx,
			createTestRequestMetadata(reportMetadata),
			req,
		)
		require.NoError(t, err)
	})
	t.Run("Invalid remaining account public key length", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		req := buildWriteReportReq(t, helper.forwarderState, reportMetadata, key.PublicKey())
		req.RemainingAccounts = []*solcap.AccountMeta{
			{PublicKey: []byte{1, 2, 3}, IsWritable: false},
			{PublicKey: helper.forwarderState.Bytes()},
		}
		req.Report.RawReport = buildRawReport(t, reportMetadata, computeAccountHash(req.RemainingAccounts), nil)
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "remaining account 0")
		require.Contains(t, err.Error(), "32 bytes")
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
	t.Run("Nil remaining account meta", func(t *testing.T) {
		reportMetadata := createTestReportMetadata()
		req := buildWriteReportReq(t, helper.forwarderState, reportMetadata, key.PublicKey())
		req.RemainingAccounts = []*solcap.AccountMeta{nil, {PublicKey: helper.forwarderState.Bytes()}}
		req.Report.RawReport = buildRawReport(t, reportMetadata, computeAccountHash(req.RemainingAccounts), nil)
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "remaining account 0: nil account meta")
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

// TestWriteReport_PayloadValidation covers the checks that mirror the on-chain forwarder:
// the account hash, the shape of the remaining accounts list, and the receiver account itself.
func TestWriteReport_PayloadValidation(t *testing.T) {
	t.Parallel()

	newRequest := func(t *testing.T, helper *testHelper) (ocrtypes.Metadata, solana.PublicKey, *solcap.WriteReportRequest) {
		t.Helper()
		receiverKey, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)
		reportMetadata := createTestReportMetadata()
		return reportMetadata, receiverKey.PublicKey(), buildWriteReportReq(t, helper.forwarderState, reportMetadata, receiverKey.PublicKey())
	}

	t.Run("Remaining accounts hash mismatch", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		reportMetadata, _, req := newRequest(t, helper)
		// Swap in a different trailing account without updating the hash in the raw report.
		req.RemainingAccounts[2] = &solcap.AccountMeta{PublicKey: RandomBytes(solana.PublicKeyLength)}

		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "remaining account hash mismatch")
		require.Equal(t, caperrors.OriginUser, err.Origin())
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("Too few remaining accounts", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		reportMetadata, _, req := newRequest(t, helper)
		// forwarder_state alone: the forwarder always needs forwarder_authority as well.
		req.RemainingAccounts = req.RemainingAccounts[:1]
		req.Report.RawReport = buildRawReport(t, reportMetadata, computeAccountHash(req.RemainingAccounts), nil)

		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "expected accounts meta length > 2, got: 1")
		require.Equal(t, caperrors.OriginUser, err.Origin())
	})

	t.Run("Forwarder state does not match configuration", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		reportMetadata, _, req := newRequest(t, helper)
		otherState := solana.PublicKey(RandomBytes(solana.PublicKeyLength))
		req.RemainingAccounts[0] = &solcap.AccountMeta{PublicKey: otherState.Bytes()}
		req.Report.RawReport = buildRawReport(t, reportMetadata, computeAccountHash(req.RemainingAccounts), nil)

		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprintf("forwarder state from remainings accounts list %s doesn't match configured forwarder state %s", otherState, helper.forwarderState))
		require.Equal(t, caperrors.OriginUser, err.Origin())
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("Receiver account does not exist", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		reportMetadata, _, req := newRequest(t, helper)
		helper.solanaService.On("GetAccountInfoWithOpts", mock.Anything, mock.Anything).
			Return(&soltypes.GetAccountInfoReply{Value: nil}, nil)

		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "receiver account does not exist")
		require.Equal(t, caperrors.OriginUser, err.Origin())
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("Receiver account is not executable", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		reportMetadata, receiver, req := newRequest(t, helper)
		helper.solanaService.On("GetAccountInfoWithOpts", mock.Anything, mock.MatchedBy(func(r soltypes.GetAccountInfoRequest) bool {
			return r.Account == soltypes.PublicKey(receiver)
		})).Return(&soltypes.GetAccountInfoReply{Value: &soltypes.Account{Executable: false}}, nil)

		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "receiver account is non-executable")
		require.Equal(t, caperrors.OriginUser, err.Origin())
		helper.creForwarderClient.AssertNotCalled(t, "InvokeOnReport", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("Receiver account not found is a user error", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		reportMetadata, _, req := newRequest(t, helper)
		helper.solanaService.On("GetAccountInfoWithOpts", mock.Anything, mock.Anything).
			Return((*soltypes.GetAccountInfoReply)(nil), rpc.ErrNotFound)

		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "receiver account does not exist")
		require.Equal(t, caperrors.OriginUser, err.Origin())
	})

	t.Run("Receiver account lookup failure is a system error", func(t *testing.T) {
		t.Parallel()
		helper := createMocksAndCapability(t, logger.Test(t))
		reportMetadata, _, req := newRequest(t, helper)
		helper.solanaService.On("GetAccountInfoWithOpts", mock.Anything, mock.Anything).
			Return((*soltypes.GetAccountInfoReply)(nil), errors.New("rpc unavailable"))

		_, err := helper.solana.WriteReport(t.Context(), createTestRequestMetadata(reportMetadata), req)
		require.Error(t, err)
		require.Contains(t, err.Error(), "rpc unavailable")
		// An unreachable RPC is not the caller's fault, so it must not be reported as a user error.
		require.Equal(t, caperrors.OriginSystem, err.Origin())
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
		helper.transmissionInfoProvider.On("GetTransmissionInfo", mock.Anything, mock.Anything).Return(TransmissionInfo{}, errors.New(expectedError))
		helper.expectReceiverIsProgram(key.PublicKey())
		_, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata),
			buildWriteReportReq(t, helper.forwarderState, reportMetadata, key.PublicKey()))
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

		result, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), helper.validWriteReportReq(t, reportMetadata))
		require.NoError(t, err)
		require.Empty(t, result.ResponseMetadata.Metering)
	})
	t.Run("TX first transmission - Successful TX execution", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		helper := createMocksAndCapability(t, testLogger)

		receiverAddress := key.PublicKey()
		reportMetadata := createTestReportMetadata()

		helper.expectReceiverIsProgram(receiverAddress)
		writeReportRequest := buildWriteReportReq(t, helper.forwarderState, reportMetadata, receiverAddress)
		signedReport := writeReportRequest.Report
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

// createTestRemainingAccounts builds the account list the on-chain forwarder expects:
// meta[0] is forwarder_state, meta[1] is forwarder_authority, followed by extra accounts.
func createTestRemainingAccounts(forwarderState solana.PublicKey, extra int) []*solcap.AccountMeta {
	accounts := []*solcap.AccountMeta{
		{PublicKey: forwarderState.Bytes()},
		{PublicKey: RandomBytes(solana.PublicKeyLength)},
	}
	for range extra {
		accounts = append(accounts, &solcap.AccountMeta{PublicKey: RandomBytes(solana.PublicKeyLength)})
	}
	return accounts
}

// buildWriteReportReq builds a request whose remaining accounts and embedded account hash
// are consistent, i.e. one that passes payload validation. It sets up no mocks.
func buildWriteReportReq(t *testing.T, forwarderState solana.PublicKey, metadata ocrtypes.Metadata, receiver solana.PublicKey) *solcap.WriteReportRequest {
	t.Helper()
	accounts := createTestRemainingAccounts(forwarderState, 1)
	return &solcap.WriteReportRequest{
		Receiver:          receiver.Bytes(),
		RemainingAccounts: accounts,
		Report: &workflowpb.ReportResponse{
			RawReport:     buildRawReport(t, metadata, computeAccountHash(accounts), []byte("some payload")),
			ReportContext: RandomBytes(reportContextLen),
			Sigs:          generateRandomSignatures(),
		},
	}
}

// buildRawReport mirrors the on-chain report layout:
//
//	[109 bytes OCR3 metadata][32 bytes account_hash][4 bytes LE payload_len][payload...]
//
// This is how the keystone-forwarder deserializes ForwarderReport from rawReport[METADATA_LENGTH..].
func buildRawReport(t *testing.T, metadata ocrtypes.Metadata, accountHash [32]byte, payload []byte) []byte {
	t.Helper()
	header, err := metadata.Encode()
	require.NoError(t, err)
	require.Len(t, header, ocrtypes.MetadataLen)

	// Borsh-encode ForwarderReport: fixed [u8;32] hash + Vec<u8> payload (4-byte LE length prefix)
	payloadLen := make([]byte, 4)
	binary.LittleEndian.PutUint32(payloadLen, uint32(len(payload))) //nolint:gosec // G115: test payload length is always small

	raw := make([]byte, 0, len(header)+32+4+len(payload))
	raw = append(raw, header...)
	raw = append(raw, accountHash[:]...)
	raw = append(raw, payloadLen...)
	raw = append(raw, payload...)
	return raw
}

// computeAccountHash mirrors calculateHash from the workflow WASM binary and the
// on-chain forwarder: SHA-256 over concatenated 32-byte public keys.
func computeAccountHash(accounts []*solcap.AccountMeta) [32]byte {
	var buf []byte
	for _, acc := range accounts {
		buf = append(buf, acc.GetPublicKey()...)
	}
	return sha256.Sum256(buf)
}
func createMocksAndCapability(t *testing.T, lggr logger.Logger) *testHelper {
	mockSolanaService := mocks.NewSolanaService(t)
	mockTrInfo := NewTransmissionInfoProvider_mock(t)
	mockClient := NewCREForwarderClient_mock(t)
	forwarderStateKey, err := solana.NewRandomPrivateKey()
	require.NoError(t, err)
	forwarderState := forwarderStateKey.PublicKey()
	service := &Solana{
		SolanaService:            mocks.WrapSolanaService(mockSolanaService),
		forwarderClient:          mockClient,
		transmissionInfoProvider: mockTrInfo,
		beholderProcessor:        NopBeholderProcessor{},
		messageBuilder:           monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		chainSelector:            1,
		lggr:                     logger.Sugared(lggr),
		forwarderState:           forwarderState,
	}
	require.NoError(t, service.initLimiters(limits.Factory{Logger: lggr}))
	require.NotNil(t, service.txComputeLimit)
	return &testHelper{mockSolanaService, mockTrInfo, mockClient, service, forwarderState}
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

		helper.expectReceiverIsProgram(receiverAddress)
		writeReportRequest := buildWriteReportReq(t, helper.forwarderState, reportMetadata, receiverAddress)
		signedReport := writeReportRequest.Report
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

		helper.expectReceiverIsProgram(receiverAddress)
		writeReportRequest := buildWriteReportReq(t, helper.forwarderState, reportMetadata, receiverAddress)
		signedReport := writeReportRequest.Report
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

		helper.expectReceiverIsProgram(receiverAddress)
		writeReportRequest := buildWriteReportReq(t, helper.forwarderState, reportMetadata, receiverAddress)
		signedReport := writeReportRequest.Report
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

	t.Run("Nil transaction meta in fee lookup does not fail WriteReport", func(t *testing.T) {
		ctx := t.Context()
		testLogger := logger.Test(t)
		helper := createMocksAndCapability(t, testLogger)

		receiverAddress := key.PublicKey()
		reportMetadata := createTestReportMetadata()

		helper.expectReceiverIsProgram(receiverAddress)
		writeReportRequest := buildWriteReportReq(t, helper.forwarderState, reportMetadata, receiverAddress)
		signedReport := writeReportRequest.Report
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

		helper.solanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{Meta: nil}, nil)

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

		result, err := helper.solana.WriteReport(ctx, createTestRequestMetadata(reportMetadata), helper.validWriteReportReq(t, reportMetadata))
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
			SolanaService: mocks.WrapSolanaService(mockSolanaService),
			lggr:          testLogger,
		}

		sig := solana.Signature{1, 2, 3}
		txFeeInLamports := uint64(5000)
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: txFeeInLamports},
		}, nil)

		feeInLamports, err := wr.getFee(t.Context(), sig)
		require.NoError(t, err)
		require.Equal(t, txFeeInLamports, feeInLamports)
	})

	t.Run("Handles large fee values", func(t *testing.T) {
		testLogger := logger.Test(t)
		mockSolanaService := mocks.NewSolanaService(t)

		wr := &WriteReport{
			SolanaService: mocks.WrapSolanaService(mockSolanaService),
			lggr:          testLogger,
		}

		sig := solana.Signature{4, 5, 6}
		txFeeInLamports := uint64(1_000_000_000) // 1 SOL
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(&soltypes.GetTransactionReply{
			Meta: &soltypes.TransactionMeta{Fee: txFeeInLamports},
		}, nil)

		feeInLamports, err := wr.getFee(t.Context(), sig)
		require.NoError(t, err)
		require.Equal(t, txFeeInLamports, feeInLamports)
	})

	t.Run("Returns error when GetTransaction fails", func(t *testing.T) {
		testLogger := logger.Test(t)
		mockSolanaService := mocks.NewSolanaService(t)

		wr := &WriteReport{
			SolanaService: mocks.WrapSolanaService(mockSolanaService),
			lggr:          testLogger,
		}

		sig := solana.Signature{7, 8, 9}
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(
			(*soltypes.GetTransactionReply)(nil), errors.New("rpc error"))

		_, err := wr.getFee(t.Context(), sig)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get transaction")
	})

	t.Run("Returns error when transaction response is nil", func(t *testing.T) {
		testLogger := logger.Test(t)
		mockSolanaService := mocks.NewSolanaService(t)

		wr := &WriteReport{
			SolanaService: mocks.WrapSolanaService(mockSolanaService),
			lggr:          testLogger,
		}

		sig := solana.Signature{10, 11, 12}
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(
			(*soltypes.GetTransactionReply)(nil), nil)

		_, err := wr.getFee(t.Context(), sig)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty transaction response")
	})

	t.Run("Returns error when transaction meta is nil", func(t *testing.T) {
		testLogger := logger.Test(t)
		mockSolanaService := mocks.NewSolanaService(t)

		wr := &WriteReport{
			SolanaService: mocks.WrapSolanaService(mockSolanaService),
			lggr:          testLogger,
		}

		sig := solana.Signature{13, 14, 15}
		mockSolanaService.On("GetTransaction", mock.Anything, mock.Anything).Return(
			&soltypes.GetTransactionReply{Meta: nil}, nil)

		_, err := wr.getFee(t.Context(), sig)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty transaction meta")
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
		require.Equal(t, byte(len(report.Sigs)), payload[0]) //nolint:gosec // G115: test, sig count is small
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
