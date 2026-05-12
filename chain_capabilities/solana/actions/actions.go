package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	commoncfg "github.com/smartcontractkit/chainlink-common/pkg/config"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/cresettings"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/smartcontractkit/chainlink-common/pkg/types"

	capcommon "github.com/smartcontractkit/capabilities/chain_capabilities/common"
	ts "github.com/smartcontractkit/capabilities/chain_capabilities/common/transmission_schedule"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/monitoring"
)

type Solana struct {
	types.SolanaService
	forwarderClient          CREForwarderClient
	transmissionInfoProvider TransmissionInfoProvider
	lggr                     logger.SugaredLogger
	chainSelector            uint64
	txComputeLimit           limits.BoundLimiter[uint32]
	reportSizeLimit          limits.BoundLimiter[commoncfg.Size]
	beholderProcessor        beholder.ProtoProcessor
	messageBuilder           *monitoring.MessageBuilder
	transmissionScheduler    ts.TransmissionScheduler
}

func NewSolana(ctx context.Context, cfg *config.Config, s types.SolanaService, messageBuilder *monitoring.MessageBuilder, beholderProcessor beholder.ProtoProcessor, lggr logger.Logger, limitsFactory limits.Factory, transmissionScheduler ts.TransmissionScheduler, chainSelector uint64) (*Solana, error) {
	client := newForwarderClient(s, lggr, cfg.CREForwarderAddress, cfg.CREForwarderState, cfg.Transmitter)
	provider, err := newOnChainTransmissionInfoProvider(ctx, cfg.CREForwarderAddress, cfg.CREForwarderState, s)
	if err != nil {
		return nil, fmt.Errorf("failed to create on-chain transmission info provider: %w", err)
	}
	sol := &Solana{
		SolanaService:            s,
		chainSelector:            chainSelector,
		lggr:                     logger.Sugared(lggr),
		forwarderClient:          client,
		transmissionInfoProvider: provider,
		messageBuilder:           messageBuilder,
		beholderProcessor:        beholderProcessor,
		transmissionScheduler:    transmissionScheduler,
	}

	return sol, sol.initLimiters(limitsFactory)
}

func (s *Solana) GetAccountInfoWithOpts(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetAccountInfoWithOptsRequest) (*capabilities.ResponseAndMetadata[*solcap.GetAccountInfoWithOptsReply], caperrors.Error) {
	// TODO
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) GetBalance(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetBalanceRequest) (*capabilities.ResponseAndMetadata[*solcap.GetBalanceReply], caperrors.Error) {
	// TODO
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) GetBlock(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetBlockRequest) (*capabilities.ResponseAndMetadata[*solcap.GetBlockReply], caperrors.Error) {
	// TODO
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) GetFeeForMessage(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetFeeForMessageRequest) (*capabilities.ResponseAndMetadata[*solcap.GetFeeForMessageReply], caperrors.Error) {
	// TODO
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) GetMultipleAccountsWithOpts(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetMultipleAccountsWithOptsRequest) (*capabilities.ResponseAndMetadata[*solcap.GetMultipleAccountsWithOptsReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) GetSignatureStatuses(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetSignatureStatusesRequest) (*capabilities.ResponseAndMetadata[*solcap.GetSignatureStatusesReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) GetSlotHeight(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetSlotHeightRequest) (*capabilities.ResponseAndMetadata[*solcap.GetSlotHeightReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) GetTransaction(
	ctx context.Context,
	metadata capabilities.RequestMetadata,
	input *solcap.GetTransactionRequest) (*capabilities.ResponseAndMetadata[*solcap.GetTransactionReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

func (s *Solana) initLimiters(limitsFactory limits.Factory) (err error) {
	// PLEX-1920 this is initial values taken from chainlink-solana/docs/forwarder. Can be tuned later
	s.reportSizeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.Solana.ReportSizeLimit)
	if err != nil {
		return
	}

	s.txComputeLimit, err = limits.MakeUpperBoundLimiter(limitsFactory, cresettings.Default.PerWorkflow.ChainWrite.Solana.GasLimit)
	if err != nil {
		return
	}
	return
}

var GetError = capcommon.GetError
var NewUserError = capcommon.NewUserError
