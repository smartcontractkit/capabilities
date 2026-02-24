package actions

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"
)

// View executes a view (read-only) call on the Aptos chain via the capability.
// It delegates to the relayer's AptosService.View after converting the capability
// request (function string like "0x1::coin::name", arguments) to the relayer's ViewPayload.
func (s *Aptos) View(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	if input == nil {
		return nil, NewUserError(fmt.Errorf("nil ViewRequest"))
	}
	if input.Function == "" {
		return nil, NewUserError(fmt.Errorf("ViewRequest.Function is required"))
	}

	payload, err := capabilityViewToRelayerPayload(input)
	if err != nil {
		return nil, NewUserError(err)
	}

	relayerReq := &aptostypes.ViewRequest{Payload: payload}
	relayerReply, err := s.aptosService.View(ctx, *relayerReq)
	if err != nil {
		return nil, GetError(err, false)
	}

	if relayerReply == nil {
		return nil, caperrors.NewPublicSystemError(fmt.Errorf("nil ViewReply from aptos service"), caperrors.Unknown)
	}

	return &capabilities.ResponseAndMetadata[*aptoscap.ViewReply]{
		Response:         &aptoscap.ViewReply{Data: relayerReply.Data},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}, nil
}

// capabilityViewToRelayerPayload converts the capability ViewRequest (function string
// e.g. "0x1::coin::name", arguments) to the relayer's ViewPayload (Module, Function, Args).
func capabilityViewToRelayerPayload(req *aptoscap.ViewRequest) (*aptostypes.ViewPayload, error) {
	// Parse fully qualified function: address::module::function
	parts := strings.Split(req.Function, "::")
	if len(parts) != 3 {
		return nil, fmt.Errorf("ViewRequest.Function must be fully qualified (address::module::function), got %q", req.Function)
	}
	addrHex := strings.TrimPrefix(parts[0], "0x")
	if len(addrHex)%2 != 0 {
		addrHex = "0" + addrHex
	}
	addrBytes, err := hex.DecodeString(addrHex)
	if err != nil {
		return nil, fmt.Errorf("ViewRequest.Function invalid address %q: %w", parts[0], err)
	}
	var addr [32]byte
	copy(addr[32-len(addrBytes):], addrBytes)

	return &aptostypes.ViewPayload{
		Module:   aptostypes.ModuleID{Address: aptostypes.AccountAddress(addr), Name: parts[1]},
		Function: parts[2],
		Args:     req.Arguments,
	}, nil
}
