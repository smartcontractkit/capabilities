package actions

import (
	"context"
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	cltypes "github.com/smartcontractkit/chainlink-common/pkg/types"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
	typesmocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
	commontest "github.com/smartcontractkit/capabilities/chain_capabilities/common/test"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type testConsensusHandler struct {
	height *ctypes.ChainHeight
}

func (h *testConsensusHandler) Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error) {
	ch := make(chan ctypes.Reply, 1)

	switch req := request.(type) {
	case *ctypes.LockableToBlockRequest:
		rawLockedRequest := req.LockToABlock(h.height)
		ev, ok := rawLockedRequest.(*ctypes.EventuallyConsistentRequest)
		if !ok {
			return nil, fmt.Errorf("expected locked request to be an EventuallyConsistentRequest, got %T", rawLockedRequest)
		}
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

func newTestAptos(svc *typesmocks.AptosService, height *ctypes.ChainHeight) *Aptos {
	return &Aptos{
		AptosService:      svc,
		ConsensusHandler:  &testConsensusHandler{height: height},
		lggr:              logger.Sugared(logger.Nop()),
		beholderProcessor: commontest.NopBeholderProcessor{},
		messageBuilder:    monitoring.NewMessageBuilder(cltypes.ChainInfo{}, capabilities.CapabilityInfo{}, "test-node"),
	}
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

	a := newTestAptos(svc, &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321})

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

func TestView_FailsOnNilRequestAsSystemError(t *testing.T) {
	t.Parallel()

	a := newTestAptos(typesmocks.NewAptosService(t), &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321})

	_, err := a.View(context.Background(), capabilities.RequestMetadata{
		WorkflowExecutionID: "weid",
		ReferenceID:         "step-id",
	}, nil)
	require.Error(t, err)
	require.Equal(t, caperrors.OriginUser, err.Origin())
	require.Contains(t, err.Error(), "viewRequest is nil")
}

func TestView_FailsOnNonPositiveConsensusHeight(t *testing.T) {
	t.Parallel()

	a := newTestAptos(typesmocks.NewAptosService(t), &ctypes.ChainHeight{Latest: 0})

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
	require.Contains(t, err.Error(), "unexpected non-positive chain height")
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

	a := newTestAptos(svc, &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321})

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

	a := newTestAptos(typesmocks.NewAptosService(t), &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321})

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

func TestView_FailsWhenRequestedLedgerVersionOverflowsChainHeightType(t *testing.T) {
	t.Parallel()

	a := newTestAptos(typesmocks.NewAptosService(t), &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321})

	requested := uint64(math.MaxInt64) + 1
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
	require.Contains(t, err.Error(), "overflows int64")
}

func TestView_FailsOnNilViewReply(t *testing.T) {
	t.Parallel()

	svc := typesmocks.NewAptosService(t)
	svc.On("View", mock.Anything, mock.AnythingOfType("aptos.ViewRequest")).
		Return((*aptostypes.ViewReply)(nil), nil).
		Once()

	a := newTestAptos(svc, &ctypes.ChainHeight{Latest: 321, Safe: 321, Finalized: 321})

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
	require.Contains(t, err.Error(), "viewReply from aptos service is nil")
}

func TestCapabilityViewPayloadConversion_ConvertsNestedVectorStructAndGenericTags(t *testing.T) {
	t.Parallel()

	payload, err := aptoscap.ConvertViewPayloadFromProto(&aptoscap.ViewPayload{
		Module:   &aptoscap.ModuleID{Address: []byte{0x01}, Name: "coin"},
		Function: "name",
		ArgTypes: []*aptoscap.TypeTag{
			{
				Kind: aptoscap.TypeTagKind_TYPE_TAG_KIND_VECTOR,
				Value: &aptoscap.TypeTag_Vector{Vector: &aptoscap.VectorTag{
					ElementType: &aptoscap.TypeTag{
						Kind: aptoscap.TypeTagKind_TYPE_TAG_KIND_STRUCT,
						Value: &aptoscap.TypeTag_Struct{Struct: &aptoscap.StructTag{
							Address: []byte{0x02},
							Module:  "aptos_coin",
							Name:    "Coin",
							TypeParams: []*aptoscap.TypeTag{
								{
									Kind:  aptoscap.TypeTagKind_TYPE_TAG_KIND_GENERIC,
									Value: &aptoscap.TypeTag_Generic{Generic: &aptoscap.GenericTag{Index: 7}},
								},
							},
						}},
					},
				}},
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, payload)
	require.Equal(t, "name", payload.Function)
	require.Len(t, payload.ArgTypes, 1)

	vectorTag, ok := payload.ArgTypes[0].Value.(aptostypes.VectorTag)
	require.True(t, ok)
	structTag, ok := vectorTag.ElementType.Value.(aptostypes.StructTag)
	require.True(t, ok)
	require.Equal(t, "aptos_coin", structTag.Module)
	require.Equal(t, "Coin", structTag.Name)
	require.Len(t, structTag.TypeParams, 1)
	genericTag, ok := structTag.TypeParams[0].Value.(aptostypes.GenericTag)
	require.True(t, ok)
	require.EqualValues(t, 7, genericTag.Index)
}

func TestCapabilityViewPayloadConversion_RejectsInvalidPayloadInputs(t *testing.T) {
	t.Parallel()

	_, err := aptoscap.ConvertViewPayloadFromProto(nil)
	require.ErrorContains(t, err, "payload is required")

	_, err = aptoscap.ConvertViewPayloadFromProto(&aptoscap.ViewPayload{Function: "name"})
	require.ErrorContains(t, err, "payload.module is required")

	_, err = aptoscap.ConvertViewPayloadFromProto(&aptoscap.ViewPayload{
		Module:   &aptoscap.ModuleID{Name: "coin"},
		Function: "name",
	})
	require.ErrorContains(t, err, "payload.module.address is required")

	_, err = aptoscap.ConvertViewPayloadFromProto(&aptoscap.ViewPayload{
		Module: &aptoscap.ModuleID{Address: []byte{0x01}, Name: "coin"},
	})
	require.ErrorContains(t, err, "payload.function is required")

	_, err = aptoscap.ConvertViewPayloadFromProto(&aptoscap.ViewPayload{
		Module:   &aptoscap.ModuleID{Address: make([]byte, aptostypes.AccountAddressLength+1), Name: "coin"},
		Function: "name",
	})
	require.ErrorContains(t, err, "module address too long")
}

func TestCapabilityTypeTagConversion_RejectsInvalidInput(t *testing.T) {
	t.Parallel()

	_, err := aptoscap.ConvertTypeTagFromProto(nil)
	require.ErrorContains(t, err, "type tag is nil")

	_, err = aptoscap.ConvertTypeTagFromProto(&aptoscap.TypeTag{Kind: aptoscap.TypeTagKind(255)})
	require.ErrorContains(t, err, "unsupported type tag kind")

	_, err = aptoscap.ConvertTypeTagFromProto(&aptoscap.TypeTag{
		Kind: aptoscap.TypeTagKind_TYPE_TAG_KIND_STRUCT,
		Value: &aptoscap.TypeTag_Struct{Struct: &aptoscap.StructTag{
			Address: make([]byte, aptostypes.AccountAddressLength+1),
			Module:  "coin",
			Name:    "Coin",
		}},
	})
	require.ErrorContains(t, err, "struct address too long")

	_, err = aptoscap.ConvertTypeTagFromProto(&aptoscap.TypeTag{
		Kind:  aptoscap.TypeTagKind_TYPE_TAG_KIND_GENERIC,
		Value: &aptoscap.TypeTag_Generic{Generic: &aptoscap.GenericTag{Index: 1 << 16}},
	})
	require.ErrorContains(t, err, "generic type index out of range")
}
