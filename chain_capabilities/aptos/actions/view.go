package actions

import (
	"context"
	"fmt"
	"math"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
	commonMon "github.com/smartcontractkit/capabilities/libs/monitoring"
)

// View executes a view (read-only) call on the Aptos chain via the capability.
// It delegates to the relayer's AptosService.View after converting the capability
// request payload to the relayer's ViewPayload.
func (s *Aptos) View(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)

	if input == nil {
		return nil, NewUserError(fmt.Errorf("nil ViewRequest"))
	}
	if input.Payload == nil {
		return nil, NewUserError(fmt.Errorf("ViewRequest.Payload is required"))
	}

	payload, err := aptostypes.ViewPayloadFromCapability(input.Payload)
	if err != nil {
		return nil, NewUserError(err)
	}

	request := ctypes.NewLockableToBlockRequest(
		commonMon.RequestID(metadata.WorkflowExecutionID, metadata.ReferenceID),
		func(ctx context.Context, chainHeight *ctypes.ChainHeight) ([]byte, error) {
			// Aptos view requests use an explicit ledger version. If the caller does not specify one,
			// the capability uses the consensus-locked latest height for deterministic reads.
			ledgerVersion, err := resolveLedgerVersion(chainHeight, input.LedgerVersion)
			if err != nil {
				return nil, err
			}

			if s.lggr != nil {
				s.lggr.Debugw(
					"Aptos view: resolved ledger version",
					"consensus_latest", chainHeight.Latest,
					"consensus_safe", chainHeight.Safe,
					"consensus_finalized", chainHeight.Finalized,
					"requested_ledger_version", input.LedgerVersion,
					"selected_ledger_version", ledgerVersion,
				)
			}

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
