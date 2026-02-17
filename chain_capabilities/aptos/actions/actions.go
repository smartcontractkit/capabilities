package actions

import (
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
)

type Aptos struct {
	aptosService     types.AptosService
	forwarderAddress [32]byte
	lggr             logger.SugaredLogger
}

func NewAptos(cfg *config.Config, aptosService types.AptosService, lggr logger.Logger) (*Aptos, error) {
	if aptosService == nil {
		return nil, fmt.Errorf("aptos service is required")
	}

	return &Aptos{
		aptosService:     aptosService,
		forwarderAddress: cfg.CREForwarderAddress,
		lggr:             logger.Sugared(lggr),
	}, nil
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
