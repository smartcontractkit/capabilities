package actions

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/gagliardetto/solana-go"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/testing/protocmp"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	soltypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type mockedSolana struct {
	solanaService *mocks.SolanaService
	solana        *Solana
}

func TestGetAccountInfoWithOpts(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)

		_, err := helper.solana.GetAccountInfoWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, &solcap.GetAccountInfoWithOptsRequest{
			Account: solana.NewWallet().PublicKey().Bytes(),
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid account public key", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)

		_, err := helper.solana.GetAccountInfoWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, &solcap.GetAccountInfoWithOptsRequest{
			Account: []byte{1, 2, 3},
		})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
		require.Contains(t, err.Error(), "invalid request")
		require.Contains(t, err.Error(), "invalid public key")
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)

		accountKey, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		const slot uint64 = 42_000
		accountData := []byte("account-data")
		serviceReply := &soltypes.GetAccountInfoReply{
			RPCContext: soltypes.RPCContext{Slot: slot},
			Value: &soltypes.Account{
				Lamports: 1_000_000,
				Data: &soltypes.DataBytesOrJSON{
					RawDataEncoding: soltypes.EncodingBase64,
					AsDecodedBinary: accountData,
				},
			},
		}

		helper.solanaService.EXPECT().
			GetAccountInfoWithOpts(mock.Anything, mock.MatchedBy(func(req soltypes.GetAccountInfoRequest) bool {
				return req.Account == soltypes.PublicKey(accountKey.PublicKey())
			})).
			Return(serviceReply, nil).
			Once()

		resp, err := helper.solana.GetAccountInfoWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, &solcap.GetAccountInfoWithOptsRequest{
			Account: accountKey.PublicKey().Bytes(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		expectedReply, err := solcap.ConvertGetAccountInfoReplyToProto(serviceReply)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(expectedReply, resp.Response, protocmp.Transform()))
		require.NotNil(t, resp.OCRAttestation)
	})

	t.Run("solana service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)

		accountKey, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)
		expectedErr := errors.New("rpc unavailable")

		helper.solanaService.EXPECT().
			GetAccountInfoWithOpts(mock.Anything, mock.Anything).
			Return((*soltypes.GetAccountInfoReply)(nil), expectedErr).
			Once()

		_, err = helper.solana.GetAccountInfoWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, &solcap.GetAccountInfoWithOptsRequest{
			Account: accountKey.PublicKey().Bytes(),
		})
		require.Error(t, err)
		require.ErrorContains(t, err, expectedErr.Error())
	})

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)

		accountKey, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		ch := make(chan ctypes.Reply)
		helper.solana.handler = testConsensusHandler{
			handle: func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
				return ch, nil
			},
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err = helper.solana.GetAccountInfoWithOpts(ctx, capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, &solcap.GetAccountInfoWithOptsRequest{
			Account: accountKey.PublicKey().Bytes(),
		})
		require.Error(t, err)
		require.ErrorContains(t, err, context.Canceled.Error())
	})
}

func newMockedSolana(t *testing.T, readsEnabled bool) *mockedSolana {
	t.Helper()

	mockSolanaService := mocks.NewSolanaService(t)
	lggr := logger.Test(t)
	service := &Solana{
		readsEnabled:      readsEnabled,
		SolanaService:     mocks.WrapSolanaService(mockSolanaService),
		beholderProcessor: NopBeholderProcessor{},
		messageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		chainSelector:     1,
		lggr:              logger.Sugared(lggr),
		handler: testConsensusHandler{
			handle: runVolatileHashableHandle,
		},
	}
	require.NoError(t, service.initLimiters(limits.Factory{Logger: lggr}))

	return &mockedSolana{
		solanaService: mockSolanaService,
		solana:        service,
	}
}

type testConsensusHandler struct {
	handle func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error)
}

func (h testConsensusHandler) Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error) {
	return h.handle(ctx, request)
}

func runVolatileHashableHandle(ctx context.Context, req ctypes.Request) (<-chan ctypes.Reply, error) {
	observableRequest, ok := req.(ctypes.ObservableRequest)
	if !ok {
		return nil, fmt.Errorf("request is not an ObservableRequest")
	}

	_ = observableRequest.CaptureObservation(ctx)
	observation, err := observableRequest.GetOCRObservation()
	if err != nil {
		return nil, fmt.Errorf("failed to get OCR observation: %w", err)
	}
	if observation == nil {
		ch := make(chan ctypes.Reply, 1)
		ch <- ctypes.Reply{Err: fmt.Errorf("no observation captured")}
		return ch, nil
	}

	var reply ctypes.Reply
	switch tObservation := observation.Observation.(type) {
	case *ctypes.RequestObservation_Volatile:
		vol := tObservation.Volatile
		if vol == nil {
			return nil, fmt.Errorf("nil volatile observation")
		}
		if len(vol.Observations) == 0 {
			if len(vol.Error) > 0 {
				reply = ctypes.Reply{Err: ctypes.ObservationError(vol.Error).Err()}
			} else {
				reply = ctypes.Reply{Err: fmt.Errorf("no volatile observations")}
			}
		} else {
			vo := vol.Observations[0]
			if len(vo.Hash) != ctypes.HashLength {
				return nil, fmt.Errorf("unexpected hash length: got %d, want %d", len(vo.Hash), ctypes.HashLength)
			}
			var reportData ctypes.Hash
			copy(reportData[:], vo.Hash)
			reply = ctypes.Reply{Value: ctypes.NewHashableRequestReport(ocrtypes.ConfigDigest{}, 0, reportData, nil)}
		}
	default:
		return nil, fmt.Errorf("unexpected observation type: %T", observation.Observation)
	}

	ch := make(chan ctypes.Reply, 1)
	ch <- reply
	return ch, nil
}

// runECHashableHandle simulates OCR consensus for ECHashableRequest (EventuallyConsistent).
func runECHashableHandle(ctx context.Context, req ctypes.Request) (<-chan ctypes.Reply, error) {
	observableRequest, ok := req.(ctypes.ObservableRequest)
	if !ok {
		return nil, fmt.Errorf("request is not an ObservableRequest")
	}
	_ = observableRequest.CaptureObservation(ctx)
	observation, err := observableRequest.GetOCRObservation()
	if err != nil {
		return nil, fmt.Errorf("failed to get OCR observation: %w", err)
	}
	if observation == nil {
		ch := make(chan ctypes.Reply, 1)
		ch <- ctypes.Reply{Err: fmt.Errorf("no observation captured")}
		return ch, nil
	}

	var reply ctypes.Reply
	switch tObs := observation.Observation.(type) {
	case *ctypes.RequestObservation_Hashable:
		hashBytes := tObs.Hashable
		if len(hashBytes) != ctypes.HashLength {
			return nil, fmt.Errorf("unexpected hash length: got %d, want %d", len(hashBytes), ctypes.HashLength)
		}
		var reportData ctypes.Hash
		copy(reportData[:], hashBytes)
		reply = ctypes.Reply{Value: ctypes.NewHashableRequestReport(ocrtypes.ConfigDigest{}, 0, reportData, nil)}
	case *ctypes.RequestObservation_Error:
		reply = ctypes.Reply{Err: ctypes.ObservationError(tObs.Error).Err()}
	default:
		return nil, fmt.Errorf("unexpected observation type: %T", observation.Observation)
	}

	ch := make(chan ctypes.Reply, 1)
	ch <- reply
	return ch, nil
}

// runAggregatableHandle simulates OCR consensus for AggregatableRequest (Volatile Aggregatable).
// It passes the observed Decimal value straight through as the "aggregated" result.
func runAggregatableHandle(ctx context.Context, req ctypes.Request) (<-chan ctypes.Reply, error) {
	observableRequest, ok := req.(ctypes.ObservableRequest)
	if !ok {
		return nil, fmt.Errorf("request is not an ObservableRequest")
	}
	_ = observableRequest.CaptureObservation(ctx)
	observation, err := observableRequest.GetOCRObservation()
	if err != nil {
		return nil, fmt.Errorf("failed to get OCR observation: %w", err)
	}
	if observation == nil {
		ch := make(chan ctypes.Reply, 1)
		ch <- ctypes.Reply{Err: fmt.Errorf("no observation captured")}
		return ch, nil
	}

	var reply ctypes.Reply
	switch tObs := observation.Observation.(type) {
	case *ctypes.RequestObservation_Aggregatable:
		if tObs.Aggregatable == nil {
			return nil, fmt.Errorf("nil aggregatable observation")
		}
		reply = ctypes.Reply{Value: tObs.Aggregatable.Value}
	case *ctypes.RequestObservation_Error:
		reply = ctypes.Reply{Err: ctypes.ObservationError(tObs.Error).Err()}
	default:
		return nil, fmt.Errorf("unexpected observation type: %T", observation.Observation)
	}

	ch := make(chan ctypes.Reply, 1)
	ch <- reply
	return ch, nil
}

// validSig returns a valid 64-byte signature slice.
func validSig() []byte {
	sig := make([]byte, soltypes.SignatureLength)
	for i := range sig {
		sig[i] = byte(i + 1)
	}
	return sig
}

// ---- GetBalance ----

func TestGetBalance(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		_, err := helper.solana.GetBalance(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBalanceRequest{Addr: solana.NewWallet().PublicKey().Bytes()})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid addr", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		_, err := helper.solana.GetBalance(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBalanceRequest{Addr: []byte{1, 2, 3}})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
		require.Contains(t, err.Error(), "invalid request")
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		helper.solanaService.EXPECT().
			GetBalance(mock.Anything, mock.MatchedBy(func(req soltypes.GetBalanceRequest) bool {
				return req.Addr == soltypes.PublicKey(key.PublicKey())
			})).
			Return(&soltypes.GetBalanceReply{Value: 1_000_000}, nil).Once()

		resp, err := helper.solana.GetBalance(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBalanceRequest{Addr: key.PublicKey().Bytes()})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, uint64(1_000_000), resp.Response.Value)
		require.NotNil(t, resp.OCRAttestation)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)
		svcErr := errors.New("rpc down")

		helper.solanaService.EXPECT().
			GetBalance(mock.Anything, mock.Anything).
			Return((*soltypes.GetBalanceReply)(nil), svcErr).Once()

		_, err = helper.solana.GetBalance(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBalanceRequest{Addr: key.PublicKey().Bytes()})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}

// ---- GetMultipleAccountsWithOpts ----

func TestGetMultipleAccountsWithOpts(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		_, err := helper.solana.GetMultipleAccountsWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetMultipleAccountsWithOptsRequest{
			Accounts: [][]byte{solana.NewWallet().PublicKey().Bytes()},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid account key", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		_, err := helper.solana.GetMultipleAccountsWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetMultipleAccountsWithOptsRequest{
			Accounts: [][]byte{{1, 2, 3}},
		})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		serviceReply := &soltypes.GetMultipleAccountsReply{
			RPCContext: soltypes.RPCContext{Slot: 77},
			Value:      []*soltypes.Account{{Lamports: 500}},
		}
		helper.solanaService.EXPECT().
			GetMultipleAccountsWithOpts(mock.Anything, mock.Anything).
			Return(serviceReply, nil).Once()

		resp, err := helper.solana.GetMultipleAccountsWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetMultipleAccountsWithOptsRequest{
			Accounts: [][]byte{key.PublicKey().Bytes()},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Response.Value, 1)
		require.NotNil(t, resp.OCRAttestation)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)
		svcErr := errors.New("rpc timeout")

		helper.solanaService.EXPECT().
			GetMultipleAccountsWithOpts(mock.Anything, mock.Anything).
			Return((*soltypes.GetMultipleAccountsReply)(nil), svcErr).Once()

		_, err = helper.solana.GetMultipleAccountsWithOpts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetMultipleAccountsWithOptsRequest{
			Accounts: [][]byte{key.PublicKey().Bytes()},
		})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}

// ---- GetProgramAccounts ----

func TestGetProgramAccounts(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		_, err := helper.solana.GetProgramAccounts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetProgramAccountsRequest{Program: solana.NewWallet().PublicKey().Bytes()})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid program key", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		_, err := helper.solana.GetProgramAccounts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetProgramAccountsRequest{Program: []byte{1, 2, 3}})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
		require.Contains(t, err.Error(), "invalid request")
	})

	t.Run("nil filter entry", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)
		_, err = helper.solana.GetProgramAccounts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetProgramAccountsRequest{
			Program: key.PublicKey().Bytes(),
			Opts:    &solcap.GetProgramAccountsOpts{Filters: []*solcap.RPCFilter{nil}},
		})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)

		serviceReply := &soltypes.GetProgramAccountsReply{
			Value: []*soltypes.KeyedAccount{
				{Pubkey: soltypes.PublicKey(key.PublicKey())},
			},
		}
		helper.solanaService.EXPECT().
			GetProgramAccounts(mock.Anything, mock.MatchedBy(func(req soltypes.GetProgramAccountsRequest) bool {
				return req.Program == soltypes.PublicKey(key.PublicKey())
			})).
			Return(serviceReply, nil).Once()

		resp, err := helper.solana.GetProgramAccounts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetProgramAccountsRequest{Program: key.PublicKey().Bytes()})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Response.Value, 1)
		require.NotNil(t, resp.OCRAttestation)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		key, err := solana.NewRandomPrivateKey()
		require.NoError(t, err)
		svcErr := errors.New("rpc unavailable")

		helper.solanaService.EXPECT().
			GetProgramAccounts(mock.Anything, mock.Anything).
			Return((*soltypes.GetProgramAccountsReply)(nil), svcErr).Once()

		_, err = helper.solana.GetProgramAccounts(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetProgramAccountsRequest{Program: key.PublicKey().Bytes()})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}

// ---- GetTransaction ----

func TestGetTransaction(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		_, err := helper.solana.GetTransaction(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetTransactionRequest{Signature: validSig()})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid signature", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		_, err := helper.solana.GetTransaction(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetTransactionRequest{Signature: []byte{1, 2, 3}})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}

		serviceReply := &soltypes.GetTransactionReply{Slot: 99}
		helper.solanaService.EXPECT().
			GetTransaction(mock.Anything, mock.Anything).
			Return(serviceReply, nil).Once()

		resp, err := helper.solana.GetTransaction(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetTransactionRequest{Signature: validSig()})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, uint64(99), resp.Response.Slot)
		require.NotNil(t, resp.OCRAttestation)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		svcErr := errors.New("tx not found")

		helper.solanaService.EXPECT().
			GetTransaction(mock.Anything, mock.Anything).
			Return((*soltypes.GetTransactionReply)(nil), svcErr).Once()

		_, err := helper.solana.GetTransaction(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetTransactionRequest{Signature: validSig()})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}

// ---- GetSignatureStatuses ----

func TestGetSignatureStatuses(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		_, err := helper.solana.GetSignatureStatuses(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSignatureStatusesRequest{Sigs: [][]byte{validSig()}})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid signature", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		_, err := helper.solana.GetSignatureStatuses(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSignatureStatusesRequest{Sigs: [][]byte{{1, 2, 3}}})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}

		conf := uint64(10)
		serviceReply := &soltypes.GetSignatureStatusesReply{
			Results: []soltypes.GetSignatureStatusesResult{
				{Slot: 42, Confirmations: &conf, ConfirmationStatus: soltypes.ConfirmationStatusConfirmed},
			},
		}
		helper.solanaService.EXPECT().
			GetSignatureStatuses(mock.Anything, mock.Anything).
			Return(serviceReply, nil).Once()

		resp, err := helper.solana.GetSignatureStatuses(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSignatureStatusesRequest{Sigs: [][]byte{validSig()}})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Response.Results, 1)
		require.Equal(t, uint64(42), resp.Response.Results[0].Slot)
		require.NotNil(t, resp.OCRAttestation)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		svcErr := errors.New("sig lookup failed")

		helper.solanaService.EXPECT().
			GetSignatureStatuses(mock.Anything, mock.Anything).
			Return((*soltypes.GetSignatureStatusesReply)(nil), svcErr).Once()

		_, err := helper.solana.GetSignatureStatuses(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSignatureStatusesRequest{Sigs: [][]byte{validSig()}})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}

// ---- GetBlock ----

func TestGetBlock(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		_, err := helper.solana.GetBlock(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBlockRequest{Slot: 1})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid commitment", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		_, err := helper.solana.GetBlock(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBlockRequest{
			Slot: 1,
			Opts: &solcap.GetBlockOpts{Commitment: solcap.CommitmentType(999)},
		})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}

		serviceReply := &soltypes.GetBlockReply{ParentSlot: 41}
		helper.solanaService.EXPECT().
			GetBlock(mock.Anything, mock.MatchedBy(func(req soltypes.GetBlockRequest) bool {
				return req.Slot == 42
			})).
			Return(serviceReply, nil).Once()

		resp, err := helper.solana.GetBlock(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBlockRequest{Slot: 42})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, uint64(41), resp.Response.ParentSlot)
		require.NotNil(t, resp.OCRAttestation)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runECHashableHandle}
		svcErr := errors.New("block not available")

		helper.solanaService.EXPECT().
			GetBlock(mock.Anything, mock.Anything).
			Return((*soltypes.GetBlockReply)(nil), svcErr).Once()

		_, err := helper.solana.GetBlock(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetBlockRequest{Slot: 42})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}

// ---- GetSlotHeight ----

func TestGetSlotHeight(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}
		_, err := helper.solana.GetSlotHeight(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSlotHeightRequest{})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid commitment", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}
		_, err := helper.solana.GetSlotHeight(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSlotHeightRequest{Commitment: solcap.CommitmentType(999)})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}

		const height uint64 = 500_000
		helper.solanaService.EXPECT().
			GetSlotHeight(mock.Anything, mock.Anything).
			Return(&soltypes.GetSlotHeightReply{Height: height}, nil).Once()

		resp, err := helper.solana.GetSlotHeight(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSlotHeightRequest{Commitment: solcap.CommitmentType_COMMITMENT_TYPE_CONFIRMED})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, height, resp.Response.Height)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}
		svcErr := errors.New("slot unavailable")

		helper.solanaService.EXPECT().
			GetSlotHeight(mock.Anything, mock.Anything).
			Return((*soltypes.GetSlotHeightReply)(nil), svcErr).Once()

		_, err := helper.solana.GetSlotHeight(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetSlotHeightRequest{})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}

// ---- GetFeeForMessage ----

func TestGetFeeForMessage(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, false)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}
		_, err := helper.solana.GetFeeForMessage(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetFeeForMessageRequest{Message: "someMsg"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid commitment", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}
		_, err := helper.solana.GetFeeForMessage(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetFeeForMessageRequest{
			Message:    "someMsg",
			Commitment: solcap.CommitmentType(999),
		})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}

		const fee uint64 = 5000
		helper.solanaService.EXPECT().
			GetFeeForMessage(mock.Anything, mock.MatchedBy(func(req soltypes.GetFeeForMessageRequest) bool {
				return req.Message == "someMsg"
			})).
			Return(&soltypes.GetFeeForMessageReply{Fee: fee}, nil).Once()

		resp, err := helper.solana.GetFeeForMessage(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetFeeForMessageRequest{Message: "someMsg"})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, fee, resp.Response.Fee)
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedSolana(t, true)
		helper.solana.handler = testConsensusHandler{handle: runAggregatableHandle}
		svcErr := errors.New("fee estimation failed")

		helper.solanaService.EXPECT().
			GetFeeForMessage(mock.Anything, mock.Anything).
			Return((*soltypes.GetFeeForMessageReply)(nil), svcErr).Once()

		_, err := helper.solana.GetFeeForMessage(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid", ReferenceID: "ref",
		}, &solcap.GetFeeForMessageRequest{Message: "someMsg"})
		require.Error(t, err)
		require.ErrorContains(t, err, svcErr.Error())
	})
}
