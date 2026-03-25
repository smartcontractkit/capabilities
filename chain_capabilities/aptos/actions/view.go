package actions

import (
	"context"
	"fmt"
	"math"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func (s *Aptos) View(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	// TODO: add Aptos read init metrics and beholder logs once read observability is wired.
	ctx = metadata.ContextWithCRE(ctx)

	if input == nil {
		return nil, capcommon.NewUserError(fmt.Errorf("viewRequest is nil"))
	}
	if input.Payload == nil {
		return nil, capcommon.NewUserError(fmt.Errorf("viewRequest.Payload is required"))
	}

	payload, err := viewPayloadFromCapability(input.Payload)
	if err != nil {
		return nil, capcommon.NewUserError(err)
	}

	request := ctypes.NewLockableToBlockRequest(capcommon.RequestID(metadata), func(ctx context.Context, chainHeight *ctypes.ChainHeight) ([]byte, error) {
		ledgerVersion, err := resolveLedgerVersion(chainHeight, input.LedgerVersion)
		if err != nil {
			return nil, err
		}

		relayerReply, err := s.AptosService.View(ctx, aptostypes.ViewRequest{
			Payload:       payload,
			LedgerVersion: &ledgerVersion,
		})
		if err != nil {
			return nil, err
		}
		if relayerReply == nil {
			return nil, fmt.Errorf("viewReply from aptos service is nil")
		}

		return relayerReply.Data, nil
	})

	data, err := capcommon.ReadType[[]byte](ctx, s.ConsensusHandler, request)
	if err != nil {
		return nil, capcommon.GetError(err, false)
	}
	if s.lggr != nil {
		// TODO: replace debug success logs with Aptos read metrics/beholder events when those are wired.
		s.lggr.Debugw("View request succeeded",
			"module", payload.Module.Name,
			"function", payload.Function,
			"requestedLedgerVersion", input.LedgerVersion,
			"responseLen", len(data),
		)
	}

	return &capabilities.ResponseAndMetadata[*aptoscap.ViewReply]{
		Response:         &aptoscap.ViewReply{Data: data},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}, nil
}

func resolveLedgerVersion(chainHeight *ctypes.ChainHeight, requestedLedgerVersion *uint64) (uint64, error) {
	if chainHeight == nil {
		return 0, fmt.Errorf("chain height is nil")
	}
	if chainHeight.Latest <= 0 {
		return 0, fmt.Errorf("unexpected non-positive chain height: %d", chainHeight.Latest)
	}

	selected := chainHeight.Latest
	if requestedLedgerVersion != nil {
		if *requestedLedgerVersion > math.MaxInt64 {
			return 0, fmt.Errorf("requested ledger version overflows int64: %d", *requestedLedgerVersion)
		}

		requested := int64(*requestedLedgerVersion)
		if chainHeight.Latest < requested {
			return 0, fmt.Errorf("requested ledger version %d is ahead of consensus latest %d", requested, chainHeight.Latest)
		}
		selected = requested
	}

	if selected < 0 {
		return 0, fmt.Errorf("resolved negative ledger version: %d", selected)
	}
	return uint64(selected), nil
}

// TODO: move Aptos capability payload/type-tag conversion into chainlink-common
// proto helpers once that follow-up is ready to merge.
func viewPayloadFromCapability(payload *aptoscap.ViewPayload) (*aptostypes.ViewPayload, error) {
	if payload == nil {
		return nil, fmt.Errorf("viewRequest.Payload is required")
	}
	if payload.Module == nil {
		return nil, fmt.Errorf("viewRequest.Payload.Module is required")
	}
	if payload.Function == "" {
		return nil, fmt.Errorf("viewRequest.Payload.Function is required")
	}
	if len(payload.Module.Address) > aptostypes.AccountAddressLength {
		return nil, fmt.Errorf("module address too long: %d", len(payload.Module.Address))
	}

	// TODO: move Aptos module address conversion into chainlink-common proto helpers
	// alongside the rest of the payload/type-tag conversion follow-up.
	var moduleAddress aptostypes.AccountAddress
	copy(moduleAddress[aptostypes.AccountAddressLength-len(payload.Module.Address):], payload.Module.Address)

	argTypes := make([]aptostypes.TypeTag, 0, len(payload.ArgTypes))
	for i, tag := range payload.ArgTypes {
		converted, err := typeTagFromCapability(tag)
		if err != nil {
			return nil, fmt.Errorf("invalid arg type at index %d: %w", i, err)
		}
		argTypes = append(argTypes, converted)
	}

	return &aptostypes.ViewPayload{
		Module: aptostypes.ModuleID{
			Address: moduleAddress,
			Name:    payload.Module.Name,
		},
		Function: payload.Function,
		ArgTypes: argTypes,
		Args:     payload.Args,
	}, nil
}

// TODO: move Aptos type-tag conversion into chainlink-common proto helpers
// alongside the rest of the payload conversion follow-up.
func typeTagFromCapability(tag *aptoscap.TypeTag) (aptostypes.TypeTag, error) {
	if tag == nil {
		return aptostypes.TypeTag{}, fmt.Errorf("type tag is nil")
	}

	switch tag.Kind {
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_BOOL:
		return aptostypes.TypeTag{Value: aptostypes.BoolTag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_U8:
		return aptostypes.TypeTag{Value: aptostypes.U8Tag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_U16:
		return aptostypes.TypeTag{Value: aptostypes.U16Tag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_U32:
		return aptostypes.TypeTag{Value: aptostypes.U32Tag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_U64:
		return aptostypes.TypeTag{Value: aptostypes.U64Tag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_U128:
		return aptostypes.TypeTag{Value: aptostypes.U128Tag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_U256:
		return aptostypes.TypeTag{Value: aptostypes.U256Tag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_ADDRESS:
		return aptostypes.TypeTag{Value: aptostypes.AddressTag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_SIGNER:
		return aptostypes.TypeTag{Value: aptostypes.SignerTag{}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_VECTOR:
		vector := tag.GetVector()
		if vector == nil {
			return aptostypes.TypeTag{}, fmt.Errorf("vector tag missing vector value")
		}
		elementType, err := typeTagFromCapability(vector.ElementType)
		if err != nil {
			return aptostypes.TypeTag{}, err
		}
		return aptostypes.TypeTag{Value: aptostypes.VectorTag{ElementType: elementType}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_STRUCT:
		structTag := tag.GetStruct()
		if structTag == nil {
			return aptostypes.TypeTag{}, fmt.Errorf("struct tag missing struct value")
		}
		if len(structTag.Address) > aptostypes.AccountAddressLength {
			return aptostypes.TypeTag{}, fmt.Errorf("struct address too long: %d", len(structTag.Address))
		}

		var structAddress aptostypes.AccountAddress
		copy(structAddress[aptostypes.AccountAddressLength-len(structTag.Address):], structTag.Address)

		typeParams := make([]aptostypes.TypeTag, 0, len(structTag.TypeParams))
		for i, tp := range structTag.TypeParams {
			converted, err := typeTagFromCapability(tp)
			if err != nil {
				return aptostypes.TypeTag{}, fmt.Errorf("invalid struct type param at index %d: %w", i, err)
			}
			typeParams = append(typeParams, converted)
		}

		return aptostypes.TypeTag{Value: aptostypes.StructTag{
			Address:    structAddress,
			Module:     structTag.Module,
			Name:       structTag.Name,
			TypeParams: typeParams,
		}}, nil
	case aptoscap.TypeTagKind_TYPE_TAG_KIND_GENERIC:
		generic := tag.GetGeneric()
		if generic == nil {
			return aptostypes.TypeTag{}, fmt.Errorf("generic tag missing generic value")
		}
		if generic.Index > math.MaxUint16 {
			return aptostypes.TypeTag{}, fmt.Errorf("generic type index out of range: %d", generic.Index)
		}
		return aptostypes.TypeTag{Value: aptostypes.GenericTag{Index: uint16(generic.Index)}}, nil
	default:
		return aptostypes.TypeTag{}, fmt.Errorf("unsupported type tag kind: %v", tag.Kind)
	}
}
