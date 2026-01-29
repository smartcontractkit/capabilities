package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	solanatypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/metering"
)

const (
	PublicKeyLength = 32
)

// Solana implements the Solana chain capability actions.
type Solana struct {
	types.SolanaService
	chainSelector uint64
	lggr          logger.SugaredLogger
}

// NewSolana creates a new Solana actions handler.
func NewSolana(cfg config.Config, solanaService types.SolanaService, lggr logger.Logger, chainSelector uint64) (*Solana, caperrors.Error) {
	s := &Solana{
		SolanaService: solanaService,
		chainSelector: chainSelector,
		lggr:          logger.Sugared(lggr),
	}
	return s, nil
}

// GetBalance returns the balance in lamports for the given account address.
func (s *Solana) GetBalance(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetBalanceRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetBalanceReply], caperrors.Error) {
	// Check metering/funds
	if err := metering.CheckHasFunds(s.lggr, meta, metering.ActionSpendUnit, string(metering.GetBalance)); err != nil {
		return nil, NewUserError(err)
	}

	// Validate and convert address
	addr := input.GetAddr()
	if len(addr) != PublicKeyLength {
		return nil, NewUserError(fmt.Errorf("invalid address: expected %d bytes, got %d", PublicKeyLength, len(addr)))
	}

	var pk solanatypes.PublicKey
	copy(pk[:], addr)

	// Convert commitment type from capability proto to domain type
	commitment := convertCommitmentType(input.GetCommitment())

	// Build request
	req := solanatypes.GetBalanceRequest{
		Addr:       pk,
		Commitment: commitment,
	}

	// Call the underlying Solana service
	reply, err := s.SolanaService.GetBalance(ctx, req)
	if err != nil {
		s.lggr.Errorw("Failed to get balance", "addr", fmt.Sprintf("%x", addr), "error", err)
		return nil, GetError(err, s.isUserError(err))
	}

	s.lggr.Debugw("Successfully retrieved balance", "addr", fmt.Sprintf("%x", addr), "balance", reply.Value)

	// Build response
	responseAndMetadata := capabilities.ResponseAndMetadata[*solcap.GetBalanceReply]{
		Response:         &solcap.GetBalanceReply{Value: reply.Value},
		ResponseMetadata: metering.GetResponseMetadata(metering.GetBalance),
	}
	return &responseAndMetadata, nil
}

// GetAccountInfoWithOpts returns account information for the given account.
func (s *Solana) GetAccountInfoWithOpts(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetAccountInfoWithOptsRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetAccountInfoWithOptsReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// GetBlock returns block information for the given slot.
func (s *Solana) GetBlock(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetBlockRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetBlockReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// GetFeeForMessage returns the fee for a given message.
func (s *Solana) GetFeeForMessage(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetFeeForMessageRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetFeeForMessageReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// GetMultipleAccountsWithOpts returns account information for multiple accounts.
func (s *Solana) GetMultipleAccountsWithOpts(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetMultipleAccountsWithOptsRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetMultipleAccountsWithOptsReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// GetSignatureStatuses returns the status of signatures.
func (s *Solana) GetSignatureStatuses(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetSignatureStatusesRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetSignatureStatusesReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// GetSlotHeight returns the current slot height.
func (s *Solana) GetSlotHeight(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetSlotHeightRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetSlotHeightReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// GetTransaction returns transaction information for the given signature.
func (s *Solana) GetTransaction(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *solcap.GetTransactionRequest,
) (*capabilities.ResponseAndMetadata[*solcap.GetTransactionReply], caperrors.Error) {
	return nil, GetError(errors.New("unimplemented"), false)
}

// convertCommitmentType converts capability proto CommitmentType to domain CommitmentType.
func convertCommitmentType(ct solcap.CommitmentType) solanatypes.CommitmentType {
	switch ct {
	case solcap.CommitmentType_COMMITMENT_TYPE_FINALIZED:
		return solanatypes.CommitmentFinalized
	case solcap.CommitmentType_COMMITMENT_TYPE_CONFIRMED:
		return solanatypes.CommitmentConfirmed
	case solcap.CommitmentType_COMMITMENT_TYPE_PROCESSED:
		return solanatypes.CommitmentProcessed
	default:
		// Default to finalized for safety
		return solanatypes.CommitmentFinalized
	}
}

// isUserError determines if an error is a user error (vs system error).
func (s *Solana) isUserError(err error) bool {
	// User errors are typically validation or client-side errors
	// System errors are infrastructure/network issues
	return !errors.Is(err, context.DeadlineExceeded)
}

// GetError wraps an error as a capability error.
func GetError(err error, isUserError bool) caperrors.Error {
	if isUserError {
		return NewUserError(err)
	}
	return caperrors.NewPublicSystemError(err, caperrors.Unknown)
}

// NewUserError creates a new user error.
func NewUserError(err error) caperrors.Error {
	return caperrors.NewPublicUserError(err, caperrors.Unknown)
}
