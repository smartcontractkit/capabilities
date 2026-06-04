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
		SolanaService:     mockSolanaService,
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
