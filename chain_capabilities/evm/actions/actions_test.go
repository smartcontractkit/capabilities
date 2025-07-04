package actions_test

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"

	evmcappb "github.com/smartcontractkit/capabilities/chain_capabilities/evm/pb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"google.golang.org/protobuf/testing/protocmp"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions/mocks"
)

type nopProcessor struct{}

func (nopProcessor) Process(_ context.Context, _ proto.Message, _ ...any) error { return nil }

type evmWithMocks struct {
	actions.EVM
	evmService       *evmmock.EVMService
	consensusHandler *mocks.ConsensusHandler
}

func initMocks(t *testing.T) *evmWithMocks {
	t.Helper()
	t.Helper()
	evmSvc := evmmock.NewEVMService(t)
	consensusHandler := mocks.NewConsensusHandler(t)
	evm, err := actions.NewEVM(config.Config{}, evmSvc, commonlogger.Test(t), nopProcessor{}, &monitoring.MessageBuilder{}, consensusHandler)
	require.NoError(t, err)
	return &evmWithMocks{
		EVM:              evm,
		evmService:       evmSvc,
		consensusHandler: consensusHandler,
	}
}

func TestCapability_CallContract(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)
		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := evmcappb.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		ch := make(chan any, 1)
		ch <- []byte("ok")
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.CallContract(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Equal(t, []byte("ok"), resp.Data)
	})
	t.Run("On timeout returns error", func(t *testing.T) {
		svc := initMocks(t)
		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := evmcappb.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		ch := make(chan any, 1)
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.CallContract(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_BalanceAt(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		block := big.NewInt(123)
		ch := make(chan any, 1)
		balance, err := proto.Marshal(valuespb.NewBigIntFromInt(big.NewInt(1000)))
		require.NoError(t, err)
		ch <- balance
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.BalanceAtRequest{Account: []byte("by_account"), BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.BalanceAt(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Equal(t, int64(1000), valuespb.NewIntFromBigInt(resp.Balance).Int64())
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)
		block := big.NewInt(123)
		ch := make(chan any, 1)
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.BalanceAtRequest{Account: []byte("by_account"), BlockNumber: valuespb.NewBigIntFromInt(block)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.BalanceAt(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_FilterLogs(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		expectedReply := &evmcappb.FilterLogsReply{Logs: []*evmcappb.Log{{Address: []byte("0xabc"), Data: []byte("0xdef")}}}
		logs, err := proto.Marshal(expectedReply)
		require.NoError(t, err)
		ch <- logs
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.FilterLogsRequest{FilterQuery: &evmcappb.FilterQuery{BlockHash: make([]byte, 32)}}
		resp, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(expectedReply, resp, protocmp.Transform()))
	})
	t.Run("Returns error if both block hash and block range is used", func(t *testing.T) {
		svc := initMocks(t)
		req := &evmcappb.FilterLogsRequest{FilterQuery: &evmcappb.FilterQuery{BlockHash: bytes.Repeat([]byte{1}, 32), FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1))}}
		_, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "cannot specify both block hash and block range")
	})
	t.Run("Returns error if block hash is of invalid length", func(t *testing.T) {
		svc := initMocks(t)
		req := &evmcappb.FilterLogsRequest{FilterQuery: &evmcappb.FilterQuery{BlockHash: make([]byte, 2)}}
		_, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.FilterLogsRequest{FilterQuery: &evmcappb.FilterQuery{}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.FilterLogs(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_GetTransactionByHash(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		tx := &evmcappb.Transaction{Nonce: 12}
		transaction, err := proto.Marshal(tx)

		require.NoError(t, err)
		ch <- transaction
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionByHashRequest{Hash: make([]byte, 32)}
		resp, err := svc.GetTransactionByHash(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(evmcappb.GetTransactionByHashReply{Transaction: tx}, resp, protocmp.Transform()))
	})
	t.Run("Returns error on invalid hash", func(t *testing.T) {
		svc := initMocks(t)

		req := &evmcappb.GetTransactionByHashRequest{Hash: make([]byte, 2)}
		_, err := svc.GetTransactionByHash(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionByHashRequest{Hash: make([]byte, 32)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.GetTransactionByHash(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_GetTransactionReceipt(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		receipt := &evmcappb.Receipt{Status: 12}
		rawReceipt, err := proto.Marshal(receipt)
		require.NoError(t, err)
		ch <- rawReceipt
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionReceiptRequest{Hash: make([]byte, 32)}
		resp, err := svc.GetTransactionReceipt(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(evmcappb.GetTransactionReceiptReply{Receipt: receipt}, resp, protocmp.Transform()))
	})
	t.Run("Returns error on invalid hash", func(t *testing.T) {
		svc := initMocks(t)

		req := &evmcappb.GetTransactionReceiptRequest{Hash: make([]byte, 2)}
		_, err := svc.GetTransactionReceipt(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionReceiptRequest{Hash: make([]byte, 32)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.GetTransactionReceipt(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_EstimateGas(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		ch <- &valuespb.Decimal{
			Coefficient: valuespb.NewBigIntFromInt(big.NewInt(123)),
			Exponent:    2,
		}
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.EstimateGasRequest{Msg: &evmcappb.CallMsg{Data: []byte{0xbe, 0xef}, From: make([]byte, common.AddressLength), To: make([]byte, common.AddressLength)}}
		resp, err := svc.EstimateGas(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(evmcappb.EstimateGasReply{Gas: 12300}, resp, protocmp.Transform()))
	})
	t.Run("Returns error on invalid request", func(t *testing.T) {
		svc := initMocks(t)

		req := &evmcappb.EstimateGasRequest{Msg: nil}
		_, err := svc.EstimateGas(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "call msg can't be nil")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan any, 1)
		svc.consensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.EstimateGasRequest{Msg: &evmcappb.CallMsg{Data: []byte{0xbe, 0xef}, From: make([]byte, common.AddressLength), To: make([]byte, common.AddressLength)}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.EstimateGas(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_Register_Unregister_LogTracking(t *testing.T) {
	filterProto := &evmcappb.LPFilter{} // empty is enough for proto→types conversion

	t.Run("register happy-path", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil)

		_, err := svc.RegisterLogTracking(t.Context(), capabilities.RequestMetadata{},
			&evmcappb.RegisterLogTrackingRequest{Filter: filterProto})
		require.NoError(t, err)
	})

	t.Run("register error", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(assert.AnError)

		_, err := svc.RegisterLogTracking(t.Context(), capabilities.RequestMetadata{},
			&evmcappb.RegisterLogTrackingRequest{Filter: filterProto})
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("unregister happy-path", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("UnregisterLogTracking", mock.Anything, "myFilter").Return(nil)

		_, err := svc.UnregisterLogTracking(t.Context(), capabilities.RequestMetadata{},
			&evmcappb.UnregisterLogTrackingRequest{FilterName: "myFilter"})
		require.NoError(t, err)
	})

	t.Run("unregister error", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("UnregisterLogTracking", mock.Anything, "myFilter").Return(assert.AnError)

		_, err := svc.UnregisterLogTracking(t.Context(), capabilities.RequestMetadata{},
			&evmcappb.UnregisterLogTrackingRequest{FilterName: "myFilter"})
		assert.ErrorIs(t, err, assert.AnError)
	})
}
