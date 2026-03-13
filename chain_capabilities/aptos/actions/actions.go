package actions

import (
	"context"
	"errors"
	"fmt"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
)

// TODO: config PLEX-2598
const (
	reportSizeLimit = commoncfg.Byte * 500
)

type Aptos struct {
	types.AptosService
	forwarderClient       CREForwarderClient
	forwarderAddress      aptos_sdk.AccountAddress
	lggr                  logger.SugaredLogger
	p2pConfig             map[string]string
	chainSelector         uint64
	maxGasAmountLimit     limits.BoundLimiter[uint64]
	reportSizeLimit       limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler transmission_schedule.TransmissionScheduler
}

func NewAptos(cfg *config.Config, p2pConfig map[string]string, aptosService types.AptosService, lggr logger.Logger, limitsFactory limits.Factory, transmissionScheduler transmission_schedule.TransmissionScheduler, chainSelector uint64) (*Aptos, error) {
	if aptosService == nil {
		return nil, fmt.Errorf("aptos service is required")
	}

	fc := newForwarderClient(aptosService, lggr, cfg.CREForwarderAddress)
	forwarderAddress := aptos_sdk.AccountAddress(cfg.CREForwarderAddress)

	a := &Aptos{
		AptosService:          aptosService,
		forwarderClient:       fc,
		forwarderAddress:      forwarderAddress,
		lggr:                  logger.Sugared(lggr),
		p2pConfig:             p2pConfig,
		chainSelector:         chainSelector,
		transmissionScheduler: transmissionScheduler,
	}

	return a, a.initLimiters(limitsFactory)
}

func (a *Aptos) initLimiters(limitsFactory limits.Factory) (err error) {
	// PLEX-2599 can be tuned later
	reportSizeLimit := settings.Size(reportSizeLimit)
	a.reportSizeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, reportSizeLimit)
	if err != nil {
		return
	}

	// PLEX-2599 can be tuned later (100_000 in aptos-sdk, 200_000 in chainlink-aptos)
	maxGasAmountLimit := settings.Uint64(200_000)
	a.maxGasAmountLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, maxGasAmountLimit)
	return
}

func (a *Aptos) Close() error {
	return services.CloseAll(a.reportSizeLimit, a.maxGasAmountLimit)
}

func (a *Aptos) AccountAPTBalance(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.AccountAPTBalanceRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.AccountAPTBalanceReply], caperrors.Error) {
	return nil, capcommon.GetError(errors.New("unimplemented"), false)
}

func (a *Aptos) View(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	return nil, capcommon.GetError(errors.New("unimplemented"), false)
}

func (a *Aptos) TransactionByHash(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.TransactionByHashRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.TransactionByHashReply], caperrors.Error) {
	return nil, capcommon.GetError(errors.New("unimplemented"), false)
}

func (a *Aptos) AccountTransactions(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.AccountTransactionsRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.AccountTransactionsReply], caperrors.Error) {
	return nil, capcommon.GetError(errors.New("unimplemented"), false)
}

// Info returns the capability info for registration.
func (a *Aptos) Info() (capabilities.CapabilityInfo, error) {
	return capabilities.CapabilityInfo{}, nil
}
