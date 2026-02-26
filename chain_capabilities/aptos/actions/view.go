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

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
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

	request := ctypes.NewLockableToBlockRequest(
		commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID),
		func(ctx context.Context, chainHeight *ctypes.ChainHeight) ([]byte, error) {
			if chainHeight == nil {
				return nil, fmt.Errorf("chain height is nil")
			}
			if chainHeight.Latest < 0 {
				return nil, fmt.Errorf("unexpected negative chain height: %d", chainHeight.Latest)
			}
			ledgerVersion := uint64(chainHeight.Latest)
			relayerReq := aptostypes.ViewRequest{
				Payload:       payload,
				LedgerVersion: &ledgerVersion,
			}

			relayerReply, err := s.aptosService.View(ctx, relayerReq)
			if err != nil {
				return nil, err
			}
			if relayerReply == nil {
				return nil, fmt.Errorf("nil ViewReply from aptos service")
			}
			return relayerReply.Data, nil
		},
	)
	data, err := readType[[]byte](ctx, s.ConsensusHandler, request)
	if err != nil {
		return nil, GetError(err, false)
	}

	return &capabilities.ResponseAndMetadata[*aptoscap.ViewReply]{
		Response:         &aptoscap.ViewReply{Data: data},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}, nil
}

// capabilityViewToRelayerPayload converts the capability ViewRequest (function string
// e.g. "0x1::coin::name", arguments) to the relayer's ViewPayload (Module, Function, Args).
func capabilityViewToRelayerPayload(req *aptoscap.ViewRequest) (*aptostypes.ViewPayload, error) {
	// Parse fully qualified function: address::module::function
	parts := strings.SplitN(req.Function, "::", 3)
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

	functionName, typeArgs, err := parseFunctionAndTypeArgs(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid function type args: %w", err)
	}

	argTypes := make([]aptostypes.TypeTag, 0, len(typeArgs))
	for _, ta := range typeArgs {
		tag, parseErr := parseTypeTag(ta)
		if parseErr != nil {
			return nil, fmt.Errorf("invalid type argument %q: %w", ta, parseErr)
		}
		argTypes = append(argTypes, tag)
	}

	return &aptostypes.ViewPayload{
		Module:   aptostypes.ModuleID{Address: aptostypes.AccountAddress(addr), Name: parts[1]},
		Function: functionName,
		ArgTypes: argTypes,
		Args:     req.Arguments,
	}, nil
}

func parseFunctionAndTypeArgs(function string) (string, []string, error) {
	function = strings.TrimSpace(function)
	if function == "" {
		return "", nil, fmt.Errorf("function is empty")
	}
	idx := strings.Index(function, "<")
	if idx == -1 {
		return function, nil, nil
	}
	if !strings.HasSuffix(function, ">") {
		return "", nil, fmt.Errorf("missing closing >")
	}
	name := strings.TrimSpace(function[:idx])
	if name == "" {
		return "", nil, fmt.Errorf("missing function name")
	}
	rawTypeArgs := function[idx+1 : len(function)-1]
	typeArgs, err := splitTopLevel(rawTypeArgs, ',')
	if err != nil {
		return "", nil, err
	}
	return name, typeArgs, nil
}

func splitTopLevel(input string, sep rune) ([]string, error) {
	var (
		args  []string
		start int
		depth int
		runes = []rune(input)
	)
	for i, r := range runes {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
			if depth < 0 {
				return nil, fmt.Errorf("unbalanced type argument brackets")
			}
		default:
			if r == sep && depth == 0 {
				part := strings.TrimSpace(string(runes[start:i]))
				if part == "" {
					return nil, fmt.Errorf("empty type argument")
				}
				args = append(args, part)
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, fmt.Errorf("unbalanced type argument brackets")
	}
	last := strings.TrimSpace(string(runes[start:]))
	if last == "" {
		return nil, fmt.Errorf("empty type argument")
	}
	return append(args, last), nil
}

func parseTypeTag(input string) (aptostypes.TypeTag, error) {
	input = strings.TrimSpace(input)
	switch input {
	case "bool":
		return aptostypes.TypeTag{Value: aptostypes.BoolTag{}}, nil
	case "u8":
		return aptostypes.TypeTag{Value: aptostypes.U8Tag{}}, nil
	case "u16":
		return aptostypes.TypeTag{Value: aptostypes.U16Tag{}}, nil
	case "u32":
		return aptostypes.TypeTag{Value: aptostypes.U32Tag{}}, nil
	case "u64":
		return aptostypes.TypeTag{Value: aptostypes.U64Tag{}}, nil
	case "u128":
		return aptostypes.TypeTag{Value: aptostypes.U128Tag{}}, nil
	case "u256":
		return aptostypes.TypeTag{Value: aptostypes.U256Tag{}}, nil
	case "address":
		return aptostypes.TypeTag{Value: aptostypes.AddressTag{}}, nil
	case "signer":
		return aptostypes.TypeTag{Value: aptostypes.SignerTag{}}, nil
	}

	if strings.HasPrefix(input, "vector<") && strings.HasSuffix(input, ">") {
		inner := strings.TrimSpace(input[len("vector<") : len(input)-1])
		elem, err := parseTypeTag(inner)
		if err != nil {
			return aptostypes.TypeTag{}, err
		}
		return aptostypes.TypeTag{Value: aptostypes.VectorTag{ElementType: elem}}, nil
	}

	return parseStructTag(input)
}

func parseStructTag(input string) (aptostypes.TypeTag, error) {
	var (
		base       = input
		typeParams []aptostypes.TypeTag
	)
	if idx := strings.Index(input, "<"); idx != -1 {
		if !strings.HasSuffix(input, ">") {
			return aptostypes.TypeTag{}, fmt.Errorf("missing closing > in struct type")
		}
		base = strings.TrimSpace(input[:idx])
		rawParams := input[idx+1 : len(input)-1]
		paramStrs, err := splitTopLevel(rawParams, ',')
		if err != nil {
			return aptostypes.TypeTag{}, err
		}
		typeParams = make([]aptostypes.TypeTag, 0, len(paramStrs))
		for _, p := range paramStrs {
			tt, err := parseTypeTag(p)
			if err != nil {
				return aptostypes.TypeTag{}, err
			}
			typeParams = append(typeParams, tt)
		}
	}

	parts := strings.Split(base, "::")
	if len(parts) != 3 {
		return aptostypes.TypeTag{}, fmt.Errorf("struct type must be address::module::name, got %q", input)
	}

	addrHex := strings.TrimPrefix(parts[0], "0x")
	if len(addrHex)%2 != 0 {
		addrHex = "0" + addrHex
	}
	addrBytes, err := hex.DecodeString(addrHex)
	if err != nil {
		return aptostypes.TypeTag{}, fmt.Errorf("invalid struct address %q: %w", parts[0], err)
	}
	if len(addrBytes) > aptostypes.AccountAddressLength {
		return aptostypes.TypeTag{}, fmt.Errorf("address too long: %d", len(addrBytes))
	}
	var addr aptostypes.AccountAddress
	copy(addr[aptostypes.AccountAddressLength-len(addrBytes):], addrBytes)

	return aptostypes.TypeTag{
		Value: aptostypes.StructTag{
			Address:    addr,
			Module:     parts[1],
			Name:       parts[2],
			TypeParams: typeParams,
		},
	}, nil
}
