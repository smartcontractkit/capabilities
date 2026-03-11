package actions

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type ConsensusHandler interface {
	Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error)
}

type Aptos struct {
	aptosService     types.AptosService
	ConsensusHandler ConsensusHandler
	lggr             logger.SugaredLogger
	// TODO(aptos): apply limitsFactory to enforce request rate/throughput limits
	// for Aptos actions once non-view methods are implemented in this capability.
	limitsFactory    limits.Factory
}

func NewAptos(cfg *config.Config, aptosService types.AptosService, consensusHandler ConsensusHandler, lggr logger.Logger, limitsFactory limits.Factory) (*Aptos, error) {
	// TODO(aptos): use cfg to initialize Aptos action-level knobs (including limits)
	// when write/tx/account methods move from stubs to full implementations.
	_ = cfg

	if aptosService == nil {
		return nil, fmt.Errorf("aptos service is required")
	}
	if consensusHandler == nil {
		return nil, fmt.Errorf("consensus handler is required")
	}

	a := &Aptos{
		aptosService:     aptosService,
		ConsensusHandler: consensusHandler,
		lggr:             logger.Sugared(lggr),
		limitsFactory:    limitsFactory,
	}

	return a, nil
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
	return nil
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
