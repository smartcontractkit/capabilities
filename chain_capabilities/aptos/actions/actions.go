package actions

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type ConsensusHandler interface {
	Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error)
}

type Aptos struct {
	aptosService       types.AptosService
	ConsensusHandler   ConsensusHandler
	forwarderClient    CREForwarderClient
	capabilityRegistry core.CapabilitiesRegistry
	capabilityID       string
	lggr               logger.SugaredLogger
	maxGasAmountLimit  limits.BoundLimiter[uint64]
	reportSizeLimit    limits.BoundLimiter[commoncfg.Size]
}

func NewAptos(
	cfg *config.Config,
	aptosService types.AptosService,
	consensusHandler ConsensusHandler,
	capabilityRegistry core.CapabilitiesRegistry,
	capabilityID string,
	lggr logger.Logger,
	limitsFactory limits.Factory,
) (*Aptos, error) {
	if aptosService == nil {
		return nil, fmt.Errorf("aptos service is required")
	}
	if consensusHandler == nil {
		return nil, fmt.Errorf("consensus handler is required")
	}

	fc := newForwarderClient(aptosService, lggr, cfg.CREForwarderAddress)

	a := &Aptos{
		aptosService:       aptosService,
		ConsensusHandler:   consensusHandler,
		forwarderClient:    fc,
		capabilityRegistry: capabilityRegistry,
		capabilityID:       capabilityID,
		lggr:               logger.Sugared(lggr),
	}

	return a, a.initLimiters(limitsFactory)
}

func (a *Aptos) initLimiters(limitsFactory limits.Factory) (err error) {
	// NOTE: 265B is too tight for Aptos write reports carrying data-feeds payloads
	// (current flow is ~269B). Keep this comfortably above current usage while we
	// make this configurable.
	reportSizeLimit := settings.Size(commoncfg.Byte * 512)
	a.reportSizeLimit, err = limits.MakeBoundLimiter(limitsFactory, reportSizeLimit)
	if err != nil {
		return
	}

	// Keep aligned with local Aptos devnet transaction max-gas bounds.
	maxGasAmountLimit := settings.Uint64(1_000_000)
	a.maxGasAmountLimit, err = limits.MakeBoundLimiter(limitsFactory, maxGasAmountLimit)
	return
}

func GetError(err error, isUserError bool) caperrors.Error {
	if isUserError {
		return NewUserError(err)
	}
	return caperrors.NewPublicSystemError(err, caperrors.Unknown)
}

func NewUserError(err error) caperrors.Error {
	return caperrors.NewPublicUserError(err, caperrors.Unknown)
}

// Info returns the capability info for registration.
func (a *Aptos) Info() (capabilities.CapabilityInfo, error) {
	return capabilities.CapabilityInfo{}, nil
}

func (a *Aptos) Close() error {
	return services.CloseAll(a.maxGasAmountLimit, a.reportSizeLimit)
}

func readType[T any](ctx context.Context, reader ConsensusHandler, request ctypes.Request) (T, error) {
	var zero T
	resultCh, err := reader.Handle(ctx, request)
	if err != nil {
		return zero, err
	}

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case reply := <-resultCh:
		if reply.Err != nil {
			return zero, reply.Err
		}
		data, ok := reply.Value.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected result type: expected %T, got %T", zero, reply.Value)
		}
		return data, nil
	}
}
