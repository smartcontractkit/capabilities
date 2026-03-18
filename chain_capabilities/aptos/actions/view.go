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
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
)

func (a *Aptos) view(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	if input == nil {
		return nil, capcommon.GetError(fmt.Errorf("nil ViewRequest"), false)
	}
	if input.Payload == nil {
		return nil, capcommon.GetError(fmt.Errorf("ViewRequest.Payload is required"), false)
	}

	payload, err := aptostypes.ViewPayloadFromCapability(input.Payload)
	if err != nil {
		return nil, capcommon.NewUserError(err)
	}

	request := ctypes.NewLockableToBlockRequest(
		commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID),
		func(ctx context.Context, chainHeight *ctypes.ChainHeight) ([]byte, error) {
			ledgerVersion, err := resolveLedgerVersion(chainHeight, input.LedgerVersion)
			if err != nil {
				return nil, err
			}

			reply, err := a.AptosService.View(ctx, aptostypes.ViewRequest{
				Payload:       payload,
				LedgerVersion: &ledgerVersion,
			})
			if err != nil {
				return nil, err
			}
			if reply == nil {
				return nil, fmt.Errorf("nil ViewReply from aptos service")
			}

			return reply.Data, nil
		},
	)
	data, err := readType[[]byte](ctx, a.ConsensusHandler, request)
	if err != nil {
		return nil, capcommon.GetError(err, false)
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
	if chainHeight.Latest < 0 {
		return 0, fmt.Errorf("unexpected negative chain height: %d", chainHeight.Latest)
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
