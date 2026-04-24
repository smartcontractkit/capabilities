package actions

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	aptostypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/aptos"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func (s *Aptos) View(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	ctx = metadata.ContextWithCRE(ctx)
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: metadata}
	monitoring.EmitInitiated(ctx, s.lggr, s.beholderProcessor, s.messageBuilder.BuildViewInitiated(telemetryContext, input))

	if input == nil {
		err := fmt.Errorf("viewRequest is nil")
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildViewError(telemetryContext, input, "Failed to execute View request", err.Error(), false))
		return nil, capcommon.GetError(err, false)
	}

	payload, err := aptoscap.ConvertViewPayloadFromProto(input.Payload)
	if err != nil {
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildViewError(telemetryContext, input, "Failed to View, user error due to invalid request", err.Error(), true))
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
		monitoring.LogAndEmitError(ctx, s.lggr, s.beholderProcessor,
			s.messageBuilder.BuildViewError(telemetryContext, input, "Failed to execute View request", err.Error(), false))
		return nil, capcommon.GetError(err, false)
	}
	monitoring.LogAndEmitSuccess(ctx, "Successful View execution", s.lggr, s.beholderProcessor,
		s.messageBuilder.BuildViewSuccess(telemetryContext, input, uint64(len(data))))

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
