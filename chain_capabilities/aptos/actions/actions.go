package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	aptoscap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/aptos"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"

	"github.com/smartcontractkit/chainlink-common/pkg/services"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"

	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/aptos/monitoring"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	ctypes "github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

type ConsensusHandler interface {
	// Handle returns a channel to the result of request.GetObservation().
	// This result is consistent across all nodes in the DON, even if individual RPC states differ.
	Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error)
}

type Aptos struct {
	types.AptosService
	ConsensusHandler       ConsensusHandler
	forwarderClient        CREForwarderClient
	forwarderAddress       aptos_sdk.AccountAddress
	lggr                   logger.SugaredLogger
	p2pConfig              map[string]string
	chainSelector          uint64
	maxGasAmountLimit      limits.BoundLimiter[uint64]
	reportSizeLimit        limits.BoundLimiter[commoncfg.Size]
	transmissionScheduler  ts.TransmissionScheduler
	txSearchStartingBuffer time.Duration
	beholderProcessor      beholder.ProtoProcessor
	messageBuilder         *monitoring.MessageBuilder
}

func NewAptos(cfg *config.Config, p2pConfig map[string]string, aptosService types.AptosService, consensusHandler ConsensusHandler, messageBuilder *monitoring.MessageBuilder, beholderProcessor beholder.ProtoProcessor, lggr logger.Logger, limitsFactory limits.Factory, transmissionScheduler ts.TransmissionScheduler, chainSelector uint64) (*Aptos, error) {
	if aptosService == nil {
		return nil, fmt.Errorf("aptos service is required")
	}
	if consensusHandler == nil {
		return nil, fmt.Errorf("consensus handler is required")
	}

	fc := newForwarderClient(aptosService, lggr, cfg.CREForwarderAddress)
	forwarderAddress := aptos_sdk.AccountAddress(cfg.CREForwarderAddress)

	a := &Aptos{
		AptosService:           aptosService,
		ConsensusHandler:       consensusHandler,
		forwarderClient:        fc,
		forwarderAddress:       forwarderAddress,
		lggr:                   logger.Sugared(lggr),
		p2pConfig:              p2pConfig,
		chainSelector:          chainSelector,
		transmissionScheduler:  transmissionScheduler,
		txSearchStartingBuffer: cfg.TxSearchStartingBuffer,
		beholderProcessor:      beholderProcessor,
		messageBuilder:         messageBuilder,
	}

	return a, a.initLimiters(limitsFactory)
}

func (a *Aptos) initLimiters(limitsFactory limits.Factory) (err error) {
	a.reportSizeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.Aptos.ReportSizeLimit)
	if err != nil {
		return
	}

	a.maxGasAmountLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.Aptos.GasLimit)
	if err != nil {
		return
	}
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
