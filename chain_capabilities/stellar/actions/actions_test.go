package actions

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type mockedStellar struct {
	stellarService *mocks.StellarService
	stellar        *Stellar
}

func newMockedStellar(t *testing.T) *mockedStellar {
	t.Helper()

	mockStellarService := mocks.NewStellarService(t)
	lggr := logger.Test(t)
	service := &Stellar{
		StellarService: mockStellarService,
		chainSelector:  1,
		lggr:           logger.Sugared(lggr),
		handler:        testConsensusHandler{handle: runVolatileHashableHandle},
	}

	return &mockedStellar{
		stellarService: mockStellarService,
		stellar:        service,
	}
}

func validReadContractRequest() *stellarcap.ReadContractRequest {
	return &stellarcap.ReadContractRequest{
		ContractId: "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		Function:   "balance",
	}
}

func TestReadContract(t *testing.T) {
	t.Parallel()

	t.Run("reads disabled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)

		_, err := helper.stellar.ReadContract(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, validReadContractRequest())
		require.Error(t, err)
		require.Contains(t, err.Error(), "reads are not available")
	})

	t.Run("invalid request - missing contractID", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)

		_, err := helper.stellar.ReadContract(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, &stellarcap.ReadContractRequest{Function: "balance"})
		require.Error(t, err)
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
		require.Contains(t, err.Error(), "invalid request")
	})

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)

		const ledgerSeq uint32 = 52_000
		const result = "AAAAAwAAAAA=" // base64 XDR
		helper.stellarService.EXPECT().
			ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{
				Result:         result,
				LedgerSequence: ledgerSeq,
			}, nil).
			Once()

		resp, err := helper.stellar.ReadContract(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, validReadContractRequest())
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, result, resp.Response.GetResult())
		require.Equal(t, ledgerSeq, resp.Response.GetLedgerSequence())
	})

	t.Run("service error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)

		expectedErr := errors.New("rpc boom")
		helper.stellarService.EXPECT().
			ReadContract(mock.Anything, mock.Anything).
			Return(stellartypes.ReadContractResponse{}, expectedErr).
			Once()

		_, err := helper.stellar.ReadContract(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, validReadContractRequest())
		require.Error(t, err)
		require.ErrorContains(t, err, expectedErr.Error())
	})

	t.Run("context canceled", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)

		ch := make(chan ctypes.Reply)
		helper.stellar.handler = testConsensusHandler{
			handle: func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
				return ch, nil
			},
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err := helper.stellar.ReadContract(ctx, capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, validReadContractRequest())
		require.Error(t, err)
		require.ErrorContains(t, err, context.Canceled.Error())
	})
}

// testConsensusHandler simulates the consensus handler so a single node's observation is treated
// as the agreed value (mode of one observation), exercising the volatile / F+1 / tiebreak path.
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
