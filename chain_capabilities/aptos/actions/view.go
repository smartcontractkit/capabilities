package actions

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
)

func (a *Aptos) view(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	if input == nil {
		return nil, NewUserError(fmt.Errorf("nil ViewRequest"))
	}

	payload, err := aptostypes.ViewPayloadFromCapability(input.Payload)
	if err != nil {
		return nil, NewUserError(err)
	}

	reply, err := a.AptosService.View(ctx, aptostypes.ViewRequest{
		Payload:       payload,
		LedgerVersion: input.LedgerVersion,
	})
	if err != nil {
		return nil, GetError(err, false)
	}
	if reply == nil {
		return nil, GetError(fmt.Errorf("nil ViewReply from aptos service"), false)
	}

	return &capabilities.ResponseAndMetadata[*aptoscap.ViewReply]{
		Response:         &aptoscap.ViewReply{Data: reply.Data},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}, nil
}
