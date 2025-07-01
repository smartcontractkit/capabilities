package actions_test

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"

	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	commonlogger "github.com/smartcontractkit/chainlink-common/pkg/logger"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type nopProcessor struct{}

func (nopProcessor) Process(_ context.Context, _ proto.Message, _ ...any) error { return nil }

func initMocks(t *testing.T) (actions.EVM, *evmmock.EVMService) {
	t.Helper()
	evmSvc := evmmock.NewEVMService(t)
	evm, err := actions.NewEVM(config.Config{}, evmSvc, commonlogger.Test(t), nopProcessor{}, &monitoring.MessageBuilder{})
	require.NoError(t, err)
	return evm, evmSvc
}

func TestCapability_CallContract(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc, evmSvc := initMocks(t)

		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := evmcappb.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		evmSvc.On("CallContract", mock.Anything, mock.Anything, block).
			Return([]byte("ok"), nil)

		req := &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.CallContract(context.Background(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		assert.Equal(t, []byte("ok"), resp.Data)
	})

	t.Run("nil/zero block rejected", func(t *testing.T) {
		svc, _ := initMocks(t)

		msgProto, _ := evmcappb.ConvertCallMsgToProto(&evmtypes.CallMsg{})
		_, err := svc.CallContract(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.CallContractRequest{Call: msgProto})
		assert.ErrorContains(t, err, "blockNumber must be non-zero")
	})

	t.Run("EVM error bubbles", func(t *testing.T) {
		svc, evmSvc := initMocks(t)

		msgProto, _ := evmcappb.ConvertCallMsgToProto(&evmtypes.CallMsg{})
		block := big.NewInt(1)
		evmSvc.On("CallContract", mock.Anything, mock.Anything, block).
			Return(nil, assert.AnError)

		_, err := svc.CallContract(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_FilterLogs(t *testing.T) {
	svc, evmSvc := initMocks(t)

	toFilter := func(from, to int64) *evmcappb.FilterQuery {
		return &evmcappb.FilterQuery{
			BlockHash: bytes.Repeat([]byte{0xaa}, 32),
			FromBlock: valuespb.NewBigIntFromInt(big.NewInt(from)),
			ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(to)),
			Addresses: [][]byte{bytes.Repeat([]byte{0xbb}, 20)},
		}
	}

	t.Run("missing filter query", func(t *testing.T) {
		_, err := svc.FilterLogs(context.Background(),
			capabilities.RequestMetadata{}, &evmcappb.FilterLogsRequest{})
		assert.Error(t, err)
	})

	t.Run("fromBlock greater than toBlock rejected", func(t *testing.T) {
		_, err := svc.FilterLogs(context.Background(),
			capabilities.RequestMetadata{},
			&evmcappb.FilterLogsRequest{FilterQuery: toFilter(2, 1)})
		assert.ErrorContains(t, err, "invalid range")
	})

	t.Run("happy-path", func(t *testing.T) {
		evmSvc.On("FilterLogs", mock.Anything, mock.Anything).
			Return([]*evmtypes.Log{}, nil)

		_, err := svc.FilterLogs(context.Background(),
			capabilities.RequestMetadata{},
			&evmcappb.FilterLogsRequest{FilterQuery: toFilter(1, 2)})
		require.NoError(t, err)
	})

	t.Run("EVM error bubbles", func(t *testing.T) {
		evmSvc.ExpectedCalls = nil // reset
		evmSvc.On("FilterLogs", mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		_, err := svc.FilterLogs(context.Background(),
			capabilities.RequestMetadata{},
			&evmcappb.FilterLogsRequest{FilterQuery: toFilter(1, 2)})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_BalanceAt(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc, evmSvc := initMocks(t)

		addr := bytes.Repeat([]byte{0xaa}, 20)
		block := big.NewInt(10)
		evmSvc.On("BalanceAt", mock.Anything, mock.Anything, block).
			Return(big.NewInt(42), nil)

		resp, err := svc.BalanceAt(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.BalanceAtRequest{Account: addr, BlockNumber: valuespb.NewBigIntFromInt(block)})
		require.NoError(t, err)
		got := new(big.Int).SetBytes(resp.Balance.AbsVal)
		assert.Equal(t, "42", got.String())
	})

	t.Run("zero block rejected", func(t *testing.T) {
		svc, _ := initMocks(t)
		addr := bytes.Repeat([]byte{0xbb}, 20)
		_, err := svc.BalanceAt(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.BalanceAtRequest{Account: addr, BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(0))})
		assert.ErrorContains(t, err, "invalid block number")
	})

	t.Run("EVM error", func(t *testing.T) {
		svc, evmSvc := initMocks(t)

		addr := bytes.Repeat([]byte{0xcc}, 20)
		block := big.NewInt(3)
		evmSvc.On("BalanceAt", mock.Anything, mock.Anything, block).
			Return(nil, assert.AnError)

		_, err := svc.BalanceAt(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.BalanceAtRequest{Account: addr, BlockNumber: valuespb.NewBigIntFromInt(block)})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_EstimateGas(t *testing.T) {
	msgProto, _ := evmcappb.ConvertCallMsgToProto(&evmtypes.CallMsg{Data: []byte{0xde, 0xad}})

	t.Run("happy-path", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("EstimateGas", mock.Anything, mock.Anything).Return(uint64(99), nil)

		resp, err := svc.EstimateGas(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.EstimateGasRequest{Msg: msgProto})
		require.NoError(t, err)
		assert.Equal(t, uint64(99), resp.Gas)
	})

	t.Run("EVM error", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("EstimateGas", mock.Anything, mock.Anything).Return(uint64(0), assert.AnError)

		_, err := svc.EstimateGas(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.EstimateGasRequest{Msg: msgProto})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_GetTransactionByHash(t *testing.T) {
	hash := [32]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	t.Run("happy-path", func(t *testing.T) {
		svc, evmSvc := initMocks(t)

		evmSvc.On("GetTransactionByHash", mock.Anything, mock.Anything).
			Return(&evmtypes.Transaction{
				Hash:     hash,
				Nonce:    0,
				Gas:      0,
				GasPrice: big.NewInt(0),
				Value:    big.NewInt(0),
			}, nil)

		resp, err := svc.GetTransactionByHash(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.GetTransactionByHashRequest{Hash: hash[:]})
		require.NoError(t, err)
		assert.NotNil(t, resp.Transaction)
	})

	t.Run("EVM error", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("GetTransactionByHash", mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		_, err := svc.GetTransactionByHash(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.GetTransactionByHashRequest{Hash: hash[:]})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_GetTransactionReceipt(t *testing.T) {
	hash := [32]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}

	t.Run("happy-path", func(t *testing.T) {
		svc, evmSvc := initMocks(t)

		evmSvc.On("GetTransactionReceipt", mock.Anything, mock.Anything).
			Return(&evmtypes.Receipt{
				Status:            0,
				TxHash:            hash,
				ContractAddress:   [20]byte{},
				GasUsed:           0,
				BlockHash:         [32]byte{},
				BlockNumber:       big.NewInt(0),
				TransactionIndex:  0,
				EffectiveGasPrice: big.NewInt(0),
			}, nil)

		resp, err := svc.GetTransactionReceipt(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.GetTransactionReceiptRequest{Hash: hash[:]})
		require.NoError(t, err)
		assert.NotNil(t, resp.Receipt)
	})

	t.Run("EVM error", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("GetTransactionReceipt", mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		_, err := svc.GetTransactionReceipt(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.GetTransactionReceiptRequest{Hash: hash[:]})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_LatestAndFinalizedHead(t *testing.T) {
	svc, evmSvc := initMocks(t)

	t.Run("happy-path", func(t *testing.T) {
		evmSvc.On("LatestAndFinalizedHead", mock.Anything).
			Return(evmtypes.Head{}, evmtypes.Head{}, nil)

		_, err := svc.LatestAndFinalizedHead(context.Background(),
			capabilities.RequestMetadata{}, &emptypb.Empty{})
		require.NoError(t, err)
		evmSvc.AssertExpectations(t)
	})

	t.Run("EVM error", func(t *testing.T) {
		evmSvc.ExpectedCalls = nil
		evmSvc.On("LatestAndFinalizedHead", mock.Anything).
			Return(evmtypes.Head{}, evmtypes.Head{}, assert.AnError)

		_, err := svc.LatestAndFinalizedHead(context.Background(),
			capabilities.RequestMetadata{}, &emptypb.Empty{})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_Register_Unregister_LogTracking(t *testing.T) {
	filterProto := &evmcappb.LPFilter{} // empty is enough for proto→types conversion

	t.Run("register happy-path", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil)

		_, err := svc.RegisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.RegisterLogTrackingRequest{Filter: filterProto})
		require.NoError(t, err)
	})

	t.Run("register error", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(assert.AnError)

		_, err := svc.RegisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.RegisterLogTrackingRequest{Filter: filterProto})
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("unregister happy-path", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("UnregisterLogTracking", mock.Anything, "myFilter").Return(nil)

		_, err := svc.UnregisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.UnregisterLogTrackingRequest{FilterName: "myFilter"})
		require.NoError(t, err)
	})

	t.Run("unregister error", func(t *testing.T) {
		svc, evmSvc := initMocks(t)
		evmSvc.On("UnregisterLogTracking", mock.Anything, "myFilter").Return(assert.AnError)

		_, err := svc.UnregisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&evmcappb.UnregisterLogTrackingRequest{FilterName: "myFilter"})
		assert.ErrorIs(t, err, assert.AnError)
	})
}
