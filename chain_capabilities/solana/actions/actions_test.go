package actions_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	solcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/solana"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	solanamocks "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	solanatypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/solana"

	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/solana/test"
)

type SolanaWithMocks struct {
	*actions.Solana
	SolanaService *solanamocks.SolanaService
}

func InitMocks(t *testing.T) *SolanaWithMocks {
	t.Helper()
	solanaSvc := solanamocks.NewSolanaService(t)
	lggr := logger.Test(t)
	solana, err := actions.NewSolana(config.Config{ChainID: "devnet"}, solanaSvc, lggr, 1)
	require.NoError(t, err)
	return &SolanaWithMocks{
		Solana:        solana,
		SolanaService: solanaSvc,
	}
}

func TestCapability_GetBalance(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := InitMocks(t)

		// Create a valid 32-byte address
		addr := make([]byte, 32)
		for i := range addr {
			addr[i] = byte(i)
		}

		expectedBalance := uint64(1000000000) // 1 SOL in lamports
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.MatchedBy(func(req solanatypes.GetBalanceRequest) bool {
			return req.Addr == solanatypes.PublicKey(addr) && req.Commitment == solanatypes.CommitmentFinalized
		})).Return(&solanatypes.GetBalanceReply{Value: expectedBalance}, nil).Once()

		req := &solcap.GetBalanceRequest{
			Addr:       addr,
			Commitment: solcap.CommitmentType_COMMITMENT_TYPE_FINALIZED,
		}
		resp, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, expectedBalance, resp.Response.Value)
		test.ValidateMetering(t, resp.ResponseMetadata, string(metering.GetBalance))
	})

	t.Run("happy-path-confirmed-commitment", func(t *testing.T) {
		svc := InitMocks(t)

		addr := make([]byte, 32)
		for i := range addr {
			addr[i] = byte(i + 10)
		}

		expectedBalance := uint64(5000000000) // 5 SOL
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.MatchedBy(func(req solanatypes.GetBalanceRequest) bool {
			return req.Commitment == solanatypes.CommitmentConfirmed
		})).Return(&solanatypes.GetBalanceReply{Value: expectedBalance}, nil).Once()

		req := &solcap.GetBalanceRequest{
			Addr:       addr,
			Commitment: solcap.CommitmentType_COMMITMENT_TYPE_CONFIRMED,
		}
		resp, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, expectedBalance, resp.Response.Value)
	})

	t.Run("happy-path-processed-commitment", func(t *testing.T) {
		svc := InitMocks(t)

		addr := make([]byte, 32)
		for i := range addr {
			addr[i] = byte(i + 20)
		}

		expectedBalance := uint64(2000000000) // 2 SOL
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.MatchedBy(func(req solanatypes.GetBalanceRequest) bool {
			return req.Commitment == solanatypes.CommitmentProcessed
		})).Return(&solanatypes.GetBalanceReply{Value: expectedBalance}, nil).Once()

		req := &solcap.GetBalanceRequest{
			Addr:       addr,
			Commitment: solcap.CommitmentType_COMMITMENT_TYPE_PROCESSED,
		}
		resp, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, expectedBalance, resp.Response.Value)
	})

	t.Run("no-funds", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 32)
		req := &solcap.GetBalanceRequest{Addr: addr}
		_, err := svc.GetBalance(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 1")
	})

	t.Run("invalid-address-too-short", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 20) // Too short
		req := &solcap.GetBalanceRequest{Addr: addr}
		_, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "invalid address: expected 32 bytes, got 20")
	})

	t.Run("invalid-address-too-long", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 64) // Too long
		req := &solcap.GetBalanceRequest{Addr: addr}
		_, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "invalid address: expected 32 bytes, got 64")
	})

	t.Run("invalid-address-empty", func(t *testing.T) {
		svc := InitMocks(t)
		req := &solcap.GetBalanceRequest{Addr: nil}
		_, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "invalid address: expected 32 bytes, got 0")
	})

	t.Run("service-error", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 32)
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.Anything).
			Return(nil, errors.New("RPC connection failed")).Once()

		req := &solcap.GetBalanceRequest{Addr: addr}
		_, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "RPC connection failed")
	})

	t.Run("context-timeout", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 32)
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.Anything).
			Return(nil, context.DeadlineExceeded).Once()

		req := &solcap.GetBalanceRequest{Addr: addr}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.GetBalance(ctx, test.GetMetadataWithFunds(), req)
		require.Error(t, err)
	})

	t.Run("default-commitment-when-none-specified", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 32)

		// When COMMITMENT_TYPE_NONE is specified, should default to finalized
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.MatchedBy(func(req solanatypes.GetBalanceRequest) bool {
			return req.Commitment == solanatypes.CommitmentFinalized
		})).Return(&solanatypes.GetBalanceReply{Value: 100}, nil).Once()

		req := &solcap.GetBalanceRequest{
			Addr:       addr,
			Commitment: solcap.CommitmentType_COMMITMENT_TYPE_NONE,
		}
		resp, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, uint64(100), resp.Response.Value)
	})

	t.Run("zero-balance", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 32)

		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.Anything).
			Return(&solanatypes.GetBalanceReply{Value: 0}, nil).Once()

		req := &solcap.GetBalanceRequest{Addr: addr}
		resp, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, uint64(0), resp.Response.Value)
	})

	t.Run("max-balance", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 32)

		maxBalance := ^uint64(0) // Max uint64
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.Anything).
			Return(&solanatypes.GetBalanceReply{Value: maxBalance}, nil).Once()

		req := &solcap.GetBalanceRequest{Addr: addr}
		resp, err := svc.GetBalance(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, maxBalance, resp.Response.Value)
	})

	t.Run("no-spend-limit-configured", func(t *testing.T) {
		svc := InitMocks(t)
		addr := make([]byte, 32)

		// When no spend limit is configured, the request should still be allowed (with a warning)
		svc.SolanaService.EXPECT().GetBalance(mock.Anything, mock.Anything).
			Return(&solanatypes.GetBalanceReply{Value: 1000}, nil).Once()

		req := &solcap.GetBalanceRequest{Addr: addr}
		resp, err := svc.GetBalance(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Equal(t, uint64(1000), resp.Response.Value)
	})
}
