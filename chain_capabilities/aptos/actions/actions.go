package actions

import (
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
)

type Aptos struct {
	aptosService      types.AptosService
	forwarderClient   CREForwarderClient
	lggr              logger.SugaredLogger
	maxGasAmountLimit limits.BoundLimiter[uint64]
	reportSizeLimit   limits.BoundLimiter[commoncfg.Size]
}

func NewAptos(cfg *config.Config, aptosService types.AptosService, lggr logger.Logger, limitsFactory limits.Factory) (*Aptos, error) {
	if aptosService == nil {
		return nil, fmt.Errorf("aptos service is required")
	}

	fc := newForwarderClient(aptosService, lggr, cfg.CREForwarderAddress)

	a := &Aptos{
		aptosService:    aptosService,
		forwarderClient: fc,
		lggr:            logger.Sugared(lggr),
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

	// this is arbitrary
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
