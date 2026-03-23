package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type testConsensusHandler struct {
	height *ctypes.ChainHeight
}

func (h *testConsensusHandler) Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error) {
	ch := make(chan ctypes.Reply, 1)

	switch req := request.(type) {
	case *ctypes.LockableToBlockRequest:
		ev := req.ToEventuallyConsistent(h.height)
		if err := ev.CaptureObservation(ctx); err != nil {
			ch <- ctypes.Reply{Err: err}
			return ch, nil
		}
		observation, observationErr, ok := ev.GetObservation()
		if !ok {
			ch <- ctypes.Reply{Err: fmt.Errorf("missing observation")}
			return ch, nil
		}
		if observationErr != nil {
			ch <- ctypes.Reply{Err: observationErr.Err()}
			return ch, nil
		}
		ch <- ctypes.Reply{Value: observation}
	default:
		ch <- ctypes.Reply{Err: fmt.Errorf("unsupported request type %T", request)}
	}

	return ch, nil
}

func TestView_LocksToConsensusLedgerVersion(t *testing.T) {
	t.Parallel()

	svc := typesmocks.NewAptosService(t)
	svc.On("View", mock.Anything, mock.AnythingOfType("aptos.ViewRequest")).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(aptostypes.ViewRequest)
			require.NotNil(t, req.LedgerVersion)
			require.Equal(t, uint64(321), *req.LedgerVersion)
			require.NotNil(t, req.Payload)
			require.Equal(t, "name", req.Payload.Function)
		}).
		Return(&aptostypes.ViewReply{Data: []byte(`["Aptos Coin"]`)}, nil).
		Once()

	a := &Aptos{
		AptosService:     svc,
		ConsensusHandler: &testConsensusHandler{height: &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321}},
	}

	resp, err := a.View(context.Background(), capabilities.RequestMetadata{
		WorkflowExecutionID: "weid",
		ReferenceID:         "step-id",
	}, &aptoscap.ViewRequest{
		Payload: &aptoscap.ViewPayload{
			Module:   &aptoscap.ModuleID{Address: []byte{1}, Name: "coin"},
			Function: "name",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, []byte(`["Aptos Coin"]`), resp.Response.Data)
}

func TestView_FailsOnNegativeConsensusHeight(t *testing.T) {
	t.Parallel()

	a := &Aptos{
		AptosService:     typesmocks.NewAptosService(t),
		ConsensusHandler: &testConsensusHandler{height: &ctypes.ChainHeight{Latest: -1}},
	}

	_, err := a.View(context.Background(), capabilities.RequestMetadata{
		WorkflowExecutionID: "weid",
		ReferenceID:         "step-id",
	}, &aptoscap.ViewRequest{
		Payload: &aptoscap.ViewPayload{
			Module:   &aptoscap.ModuleID{Address: []byte{1}, Name: "coin"},
			Function: "name",
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unexpected negative chain height")
}

func TestView_UsesRequestedLedgerVersionWhenProvided(t *testing.T) {
	t.Parallel()

	svc := typesmocks.NewAptosService(t)
	svc.On("View", mock.Anything, mock.AnythingOfType("aptos.ViewRequest")).
		Run(func(args mock.Arguments) {
			req := args.Get(1).(aptostypes.ViewRequest)
			require.NotNil(t, req.LedgerVersion)
			require.Equal(t, uint64(300), *req.LedgerVersion)
		}).
		Return(&aptostypes.ViewReply{Data: []byte(`["ok"]`)}, nil).
		Once()

	a := &Aptos{
		AptosService:     svc,
		ConsensusHandler: &testConsensusHandler{height: &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321}},
	}

	requested := uint64(300)
	_, err := a.View(context.Background(), capabilities.RequestMetadata{
		WorkflowExecutionID: "weid",
		ReferenceID:         "step-id",
	}, &aptoscap.ViewRequest{
		Payload: &aptoscap.ViewPayload{
			Module:   &aptoscap.ModuleID{Address: []byte{1}, Name: "coin"},
			Function: "name",
		},
		LedgerVersion: &requested,
	})
	require.NoError(t, err)
}

func TestView_FailsWhenRequestedLedgerVersionIsAheadOfConsensus(t *testing.T) {
	t.Parallel()

	a := &Aptos{
		AptosService:     typesmocks.NewAptosService(t),
		ConsensusHandler: &testConsensusHandler{height: &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321}},
	}

	requested := uint64(322)
	_, err := a.View(context.Background(), capabilities.RequestMetadata{
		WorkflowExecutionID: "weid",
		ReferenceID:         "step-id",
	}, &aptoscap.ViewRequest{
		Payload: &aptoscap.ViewPayload{
			Module:   &aptoscap.ModuleID{Address: []byte{1}, Name: "coin"},
			Function: "name",
		},
		LedgerVersion: &requested,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "ahead of consensus latest")
}

func TestCapabilityViewPayloadConversion_TypeTagConversion(t *testing.T) {
	t.Parallel()

	payload, err := viewPayloadFromCapability(&aptoscap.ViewPayload{
		Module:   &aptoscap.ModuleID{Address: []byte{0x01}, Name: "coin"},
		Function: "name",
		ArgTypes: []*aptoscap.TypeTag{{
			Kind: aptoscap.TypeTagKind_TYPE_TAG_KIND_VECTOR,
			Value: &aptoscap.TypeTag_Vector{Vector: &aptoscap.VectorTag{ElementType: &aptoscap.TypeTag{
				Kind: aptoscap.TypeTagKind_TYPE_TAG_KIND_U8,
			}}},
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.Equal(t, "name", payload.Function)
	require.Len(t, payload.ArgTypes, 1)
	_, isVector := payload.ArgTypes[0].Value.(aptostypes.VectorTag)
	require.True(t, isVector)
}

func TestCapabilityViewPayloadConversion_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	_, err := viewPayloadFromCapability(&aptoscap.ViewPayload{
		Module:   &aptoscap.ModuleID{Address: make([]byte, aptostypes.AccountAddressLength+1), Name: "coin"},
		Function: "name",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "module address too long")
}
