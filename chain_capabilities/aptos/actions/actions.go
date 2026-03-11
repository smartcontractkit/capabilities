package actions

import (
	"context"
	"errors"
	"fmt"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
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
	maxGasAmountLimit     limits.BoundLimiter[uint64]
	reportSizeLimit       limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler TransmissionScheduler
}

func NewAptos(cfg *config.Config, p2pConfig map[string]string, aptosService types.AptosService, lggr logger.Logger, limitsFactory limits.Factory, transmissionScheduler TransmissionScheduler) (*Aptos, error) {
	if aptosService == nil {
		return nil, fmt.Errorf("aptos service is required")
	}

	fc := newForwarderClient(aptosService, lggr, cfg.CREForwarderAddress)
	forwarderAddress, err := aptos_sdk.ConvertToAddress(cfg.CREForwarderAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to convert forwarder address to address: %w", err)
	}

	a := &Aptos{
		AptosService:          aptosService,
		forwarderClient:       fc,
		forwarderAddress:      *forwarderAddress,
		lggr:                  logger.Sugared(lggr),
		p2pConfig:             p2pConfig,
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

	// PLEX-2599 can be tuned later
	maxGasAmountLimit := settings.Uint64(1_000_000)
	a.maxGasAmountLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, maxGasAmountLimit)
	return
}

func (a *Aptos) Close() error {
	return services.CloseAll(a.reportSizeLimit, a.maxGasAmountLimit)
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

func (a *Aptos) AccountAPTBalance(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.AccountAPTBalanceRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.AccountAPTBalanceReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

func (a *Aptos) View(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.ViewRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.ViewReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

func (a *Aptos) TransactionByHash(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.TransactionByHashRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.TransactionByHashReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

func (a *Aptos) AccountTransactions(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *aptoscap.AccountTransactionsRequest,
) (*capabilities.ResponseAndMetadata[*aptoscap.AccountTransactionsReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// Info returns the capability info for registration.
func (a *Aptos) Info() (capabilities.CapabilityInfo, error) {
	return capabilities.CapabilityInfo{}, nil
}
