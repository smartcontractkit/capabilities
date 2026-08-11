package actions

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	stellarcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/stellar/scval"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	stellartypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/stellar"
	"github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/smartcontractkit/chainlink-framework/multinode"
	"google.golang.org/protobuf/proto"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/stellar/monitoring"
)

// nopBeholderProcessor is a no-op beholder.ProtoProcessor for tests (avoids pulling the
// common/test package's transitive telemetry deps).
type nopBeholderProcessor struct{}

func (nopBeholderProcessor) Process(context.Context, proto.Message, ...any) error { return nil }

type mockedStellar struct {
	stellarService *mocks.StellarService
	stellar        *Stellar
}

func newMockedStellar(t *testing.T) *mockedStellar {
	t.Helper()

	mockStellarService := mocks.NewStellarService(t)
	lggr := logger.Test(t)
	service := &Stellar{
		StellarService:    mockStellarService,
		chainSelector:     1,
		lggr:              logger.Sugared(lggr),
		messageBuilder:    monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
		beholderProcessor: nopBeholderProcessor{},
		handler:           testConsensusHandler{handle: runVolatileHashableHandle},
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

func TestNewStellar(t *testing.T) {
	t.Parallel()

	t.Run("nil service", func(t *testing.T) {
		t.Parallel()
		lggr := logger.Test(t)
		_, err := NewStellar(
			nil,
			"CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
			100,
			lggr,
			limits.Factory{Logger: lggr},
			ts.TransmissionScheduler{},
			1,
			testConsensusHandler{handle: runVolatileHashableHandle},
			monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
			nopBeholderProcessor{},
		)
		require.Error(t, err)
		require.Contains(t, err.Error(), "stellar service is required")
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		lggr := logger.Test(t)
		svc := mocks.NewStellarService(t)
		st, err := NewStellar(
			svc,
			"CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
			100,
			lggr,
			limits.Factory{Logger: lggr},
			ts.TransmissionScheduler{},
			1,
			testConsensusHandler{handle: runVolatileHashableHandle},
			monitoring.NewMessageBuilder(types.ChainInfo{}, capabilities.CapabilityInfo{}, ""),
			nopBeholderProcessor{},
		)
		require.NoError(t, err)
		require.NotNil(t, st)
		require.NoError(t, st.Close())
	})
}

func TestGetLatestLedger(t *testing.T) {
	t.Parallel()

	t.Run("happy path", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)
		helper.stellar.handler = testConsensusHandler{handle: runLockableToBlockHandle(&ctypes.ChainHeight{Latest: 123})}

		// The RPC returns a LedgerHeaderHistoryEntry and a LedgerCloseMeta union; the
		// capability response carries the inner LedgerHeader and the V2 close-meta arm.
		hist := xdr.LedgerHeaderHistoryEntry{
			Header: xdr.LedgerHeader{LedgerVersion: 22, LedgerSeq: 123},
		}
		headerB64, err := xdr.MarshalBase64(hist)
		require.NoError(t, err)
		wantHeaderBin, err := hist.Header.MarshalBinary()
		require.NoError(t, err)

		txSet, err := xdr.NewGeneralizedTransactionSet(1, xdr.TransactionSetV1{})
		require.NoError(t, err)
		v2 := xdr.LedgerCloseMetaV2{TxSet: txSet}
		closeMeta, err := xdr.NewLedgerCloseMeta(2, v2)
		require.NoError(t, err)
		metaB64, err := xdr.MarshalBase64(closeMeta)
		require.NoError(t, err)
		wantMetaBin, err := v2.MarshalBinary()
		require.NoError(t, err)

		const hashHex = "0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"
		wantHash, err := hex.DecodeString(hashHex)
		require.NoError(t, err)

		helper.stellarService.EXPECT().
			GetLedgers(mock.Anything, stellartypes.GetLedgersRequest{
				StartLedger: 123,
				Pagination:  &stellartypes.LedgerPaginationOptions{Limit: 1},
			}).
			Return(stellartypes.GetLedgersResponse{
				Ledgers: []stellartypes.LedgerInfo{{
					Sequence:          123,
					Hash:              hashHex,
					LedgerCloseTime:   456,
					LedgerHeaderXDR:   headerB64,
					LedgerMetadataXDR: metaB64,
				}},
			}, nil).
			Once()

		resp, err := helper.stellar.GetLatestLedger(t.Context(), capabilities.RequestMetadata{}, &stellarcap.GetLatestLedgerRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, uint32(123), resp.Response.GetSequence())
		require.Equal(t, int64(456), resp.Response.GetLedgerCloseTime())
		require.Equal(t, wantHash, resp.Response.GetHash())
		require.Equal(t, uint32(22), resp.Response.GetProtocolVersion())
		require.Equal(t, wantHeaderBin, resp.Response.GetLedgerHeaderXdr())
		require.Equal(t, wantMetaBin, resp.Response.GetLedgerMetadataXdr())
	})

	t.Run("no agreed height", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)
		helper.stellar.handler = testConsensusHandler{handle: runLockableToBlockHandle(&ctypes.ChainHeight{Latest: 0})}

		_, err := helper.stellar.GetLatestLedger(t.Context(), capabilities.RequestMetadata{}, &stellarcap.GetLatestLedgerRequest{})
		require.Error(t, err)
	})
}

func TestStellar_Info(t *testing.T) {
	t.Parallel()
	helper := newMockedStellar(t)
	info, err := helper.stellar.Info()
	require.NoError(t, err)
	require.Equal(t, capabilities.CapabilityInfo{}, info)
}

func TestReadContract(t *testing.T) {
	t.Parallel()

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
			SimulateTransaction(mock.Anything, mock.Anything).
			Return(stellartypes.SimulateTransactionResponse{
				ReturnValueXDR: result,
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

	t.Run("non-infra service error is a user error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)

		// Plain errors (e.g. invalid input surfaced by the relayer) default to user errors.
		expectedErr := errors.New("failed to decode contract id")
		helper.stellarService.EXPECT().
			SimulateTransaction(mock.Anything, mock.Anything).
			Return(stellartypes.SimulateTransactionResponse{}, expectedErr).
			Once()

		_, err := helper.stellar.ReadContract(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, validReadContractRequest())
		require.Error(t, err)
		require.ErrorContains(t, err, expectedErr.Error())
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginUser, capErr.Origin())
	})

	t.Run("node-infra service error is a system error", func(t *testing.T) {
		t.Parallel()
		helper := newMockedStellar(t)

		// Errors tagged by the relayer with multinode.ErrNodeError must survive the
		// observation-error serialization round trip and stay classified as infra/system.
		expectedErr := fmt.Errorf("failed to simulate transaction: %w", multinode.ErrNodeError)
		helper.stellarService.EXPECT().
			SimulateTransaction(mock.Anything, mock.Anything).
			Return(stellartypes.SimulateTransactionResponse{}, expectedErr).
			Once()

		_, err := helper.stellar.ReadContract(t.Context(), capabilities.RequestMetadata{
			WorkflowExecutionID: "weid",
			ReferenceID:         "step-id",
		}, validReadContractRequest())
		require.Error(t, err)
		require.ErrorContains(t, err, multinode.ErrNodeError.Error())
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr))
		require.Equal(t, caperrors.OriginSystem, capErr.Origin())
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

func TestConvertReadContractRequestFromProto(t *testing.T) {
	t.Parallel()

	t.Run("nil request", func(t *testing.T) {
		t.Parallel()
		_, err := convertReadContractRequestFromProto(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "nil")
	})

	t.Run("missing function", func(t *testing.T) {
		t.Parallel()
		_, err := convertReadContractRequestFromProto(&stellarcap.ReadContractRequest{
			ContractId: testForwarderAddress,
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "function is required")
	})

	t.Run("invalid arg conversion", func(t *testing.T) {
		t.Parallel()
		_, err := convertReadContractRequestFromProto(&stellarcap.ReadContractRequest{
			ContractId: testForwarderAddress,
			Function:   "balance",
			Args:       []*scval.ScVal{nil},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "args[0]")
	})
}

func TestIsStellarNodeInfraError_MessageSubstring(t *testing.T) {
	t.Parallel()
	require.True(t, isStellarNodeInfraError(errors.New("wrapped: "+multinode.ErrNodeError.Error())))
	require.False(t, isStellarNodeInfraError(errors.New("user input invalid")))
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

// runLockableToBlockHandle locks the request to the given agreed ChainHeight (as the
// real handler does after height agreement), then observes the resulting hashable request.
func runLockableToBlockHandle(chainHeight *ctypes.ChainHeight) func(context.Context, ctypes.Request) (<-chan ctypes.Reply, error) {
	return func(ctx context.Context, req ctypes.Request) (<-chan ctypes.Reply, error) {
		if lockable, ok := req.(interface {
			LockToABlock(*ctypes.ChainHeight) ctypes.Request
		}); ok {
			req = lockable.LockToABlock(chainHeight)
		}

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
			if len(tObs.Hashable) != ctypes.HashLength {
				return nil, fmt.Errorf("unexpected hashable length: got %d, want %d", len(tObs.Hashable), ctypes.HashLength)
			}
			var rd ctypes.Hash
			copy(rd[:], tObs.Hashable)
			reply = ctypes.Reply{Value: ctypes.NewHashableRequestReport(ocrtypes.ConfigDigest{}, 0, rd, nil)}
		case *ctypes.RequestObservation_Error:
			reply = ctypes.Reply{Err: ctypes.ObservationError(tObs.Error).Err()}
		default:
			return nil, fmt.Errorf("unexpected observation type: %T", observation.Observation)
		}

		ch := make(chan ctypes.Reply, 1)
		ch <- reply
		return ch, nil
	}
}
