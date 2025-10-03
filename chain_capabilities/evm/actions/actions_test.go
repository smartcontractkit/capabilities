package actions_test

import (
	"bytes"
	"context"
	"math/big"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"

	"google.golang.org/protobuf/testing/protocmp"

	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

func TestCapability_CallContract(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := actions.InitMocks(t)
		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := evmcappb.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		ch := make(chan types.Reply, 1)
		ch <- types.Reply{Value: []byte("ok")}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.CallContract(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, []byte("ok"), resp.Response.Data)
		test.ValidateMetering(t, resp.ResponseMetadata, string(metering.CallContract))
	})

	t.Run("no-funds", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.CallContractRequest{}
		_, err := svc.CallContract(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 2.5")
	})
	t.Run("On timeout returns error", func(t *testing.T) {
		svc := actions.InitMocks(t)
		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := evmcappb.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		ch := make(chan types.Reply, 1)
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.CallContract(ctx, test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_BalanceAt(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := actions.InitMocks(t)

		block := big.NewInt(123)
		ch := make(chan types.Reply, 1)
		balance, err := proto.Marshal(valuespb.NewBigIntFromInt(big.NewInt(1000)))
		require.NoError(t, err)
		ch <- types.Reply{Value: balance}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.BalanceAtRequest{Account: []byte("by_account"), BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.BalanceAt(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Equal(t, int64(1000), valuespb.NewIntFromBigInt(resp.Response.Balance).Int64())
		test.ValidateMetering(t, resp.ResponseMetadata, string(metering.BalanceAt))
	})

	t.Run("no-funds", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.BalanceAtRequest{}
		_, err := svc.BalanceAt(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 1")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := actions.InitMocks(t)
		block := big.NewInt(123)
		ch := make(chan types.Reply, 1)
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.BalanceAtRequest{Account: []byte("by_account"), BlockNumber: valuespb.NewBigIntFromInt(block)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.BalanceAt(ctx, test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_FilterLogs(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		expectedReply := &evmcappb.FilterLogsReply{
			Logs: []*evmcappb.Log{{Address: []byte("0xabc"), Data: []byte("0xdef")}},
		}
		logs, err := proto.Marshal(expectedReply)
		require.NoError(t, err)
		ch <- types.Reply{Value: logs}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.FilterLogsRequest{
			FilterQuery: &evmcappb.FilterQuery{
				BlockHash: make([]byte, 32),
				Topics:    []*evmcappb.Topics{},
			},
		}
		resp, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(expectedReply, resp.Response, protocmp.Transform()))
		require.Empty(t, resp.ResponseMetadata.Metering, "FilterLogs() should have one metering entry (it won't be exposed in the capabilities interface)")
	})

	t.Run("Returns error if both block hash and block range is used", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.FilterLogsRequest{
			FilterQuery: &evmcappb.FilterQuery{
				BlockHash: bytes.Repeat([]byte{1}, 32),
				FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
				Topics:    []*evmcappb.Topics{},
			},
		}
		_, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "cannot specify both block hash and block range")
	})

	t.Run("Returns error if block hash is of invalid length", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.FilterLogsRequest{
			FilterQuery: &evmcappb.FilterQuery{
				BlockHash: make([]byte, 2),
				Topics:    []*evmcappb.Topics{},
			},
		}
		_, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})

	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.FilterLogsRequest{
			FilterQuery: &evmcappb.FilterQuery{
				Topics: []*evmcappb.Topics{},
			},
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.FilterLogs(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
	t.Run("no-funds", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.FilterLogsRequest{
			FilterQuery: &evmcappb.FilterQuery{
				Topics: []*evmcappb.Topics{},
			},
		}
		_, err := svc.FilterLogs(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 2.5")
	})
}

func TestCapability_GetTransactionByHash(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		tx := &evmcappb.Transaction{Nonce: 12}
		transaction, err := proto.Marshal(tx)

		require.NoError(t, err)
		ch <- types.Reply{Value: transaction}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionByHashRequest{Hash: make([]byte, 32)}
		resp, err := svc.GetTransactionByHash(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(evmcappb.GetTransactionByHashReply{Transaction: tx}, resp.Response, protocmp.Transform()))
		test.ValidateMetering(t, resp.ResponseMetadata, string(metering.GetTransactionByHash))
	})

	t.Run("no-funds", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.GetTransactionByHashRequest{}
		_, err := svc.GetTransactionByHash(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 1")
	})
	t.Run("Returns error on invalid hash", func(t *testing.T) {
		svc := actions.InitMocks(t)

		req := &evmcappb.GetTransactionByHashRequest{Hash: make([]byte, 2)}
		_, err := svc.GetTransactionByHash(t.Context(), test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionByHashRequest{Hash: make([]byte, 32)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.GetTransactionByHash(ctx, test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_GetTransactionReceipt(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		receipt := &evmcappb.Receipt{Status: 12}
		rawReceipt, err := proto.Marshal(receipt)
		require.NoError(t, err)
		ch <- types.Reply{Value: rawReceipt}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionReceiptRequest{Hash: make([]byte, 32)}
		resp, err := svc.GetTransactionReceipt(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(evmcappb.GetTransactionReceiptReply{Receipt: receipt}, resp.Response, protocmp.Transform()))
		test.ValidateMetering(t, resp.ResponseMetadata, string(metering.GetTransactionReceipt))
	})

	t.Run("no-funds", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.GetTransactionReceiptRequest{}
		_, err := svc.GetTransactionReceipt(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 1")
	})
	t.Run("Returns error on invalid hash", func(t *testing.T) {
		svc := actions.InitMocks(t)

		req := &evmcappb.GetTransactionReceiptRequest{Hash: make([]byte, 2)}
		_, err := svc.GetTransactionReceipt(t.Context(), test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.GetTransactionReceiptRequest{Hash: make([]byte, 32)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.GetTransactionReceipt(ctx, test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_EstimateGas(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		ch <- types.Reply{
			Value: &valuespb.Decimal{
				Coefficient: valuespb.NewBigIntFromInt(big.NewInt(123)),
				Exponent:    2,
			},
		}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.EstimateGasRequest{Msg: &evmcappb.CallMsg{Data: []byte{0xbe, 0xef}, From: make([]byte, common.AddressLength), To: make([]byte, common.AddressLength)}}
		resp, err := svc.EstimateGas(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(evmcappb.EstimateGasReply{Gas: 12300}, resp.Response, protocmp.Transform()))
		test.ValidateMetering(t, resp.ResponseMetadata, string(metering.EstimateGas))
	})

	t.Run("no-funds", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.EstimateGasRequest{}
		_, err := svc.EstimateGas(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 1")
	})
	t.Run("Returns error on invalid request", func(t *testing.T) {
		svc := actions.InitMocks(t)

		req := &evmcappb.EstimateGasRequest{Msg: nil}
		_, err := svc.EstimateGas(t.Context(), test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "call msg can't be nil")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := actions.InitMocks(t)

		ch := make(chan types.Reply, 1)
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.EstimateGasRequest{Msg: &evmcappb.CallMsg{Data: []byte{0xbe, 0xef}, From: make([]byte, common.AddressLength), To: make([]byte, common.AddressLength)}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.EstimateGas(ctx, test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_HeaderByNumber(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := actions.InitMocks(t)

		block := big.NewInt(123)
		ch := make(chan types.Reply, 1)
		header := evmtypes.Header{
			Timestamp: 123,
			Number:    block,
		}

		h, err := evmcappb.ConvertHeaderToProto(&header)
		require.NoError(t, err)

		expectedReply := &evmcappb.HeaderByNumberReply{Header: h}
		asProto, err := proto.Marshal(expectedReply)
		require.NoError(t, err)
		ch <- types.Reply{Value: asProto}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.HeaderByNumberRequest{BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.HeaderByNumber(t.Context(), test.GetMetadataWithFunds(), req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(expectedReply, resp.Response, protocmp.Transform()))
		test.ValidateMetering(t, resp.ResponseMetadata, string(metering.HeaderByNumber))
	})

	t.Run("no-funds", func(t *testing.T) {
		svc := actions.InitMocks(t)
		req := &evmcappb.HeaderByNumberRequest{}
		_, err := svc.HeaderByNumber(t.Context(), test.GetMetadataWithNoFunds(), req)
		require.ErrorContains(t, err, "insufficient CRE funds: current limit is 0, action spend 1")
	})
	t.Run("On timeout returns error", func(t *testing.T) {
		svc := actions.InitMocks(t)

		block := big.NewInt(123)
		ch := make(chan types.Reply, 1)
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.HeaderByNumberRequest{BlockNumber: valuespb.NewBigIntFromInt(block)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.HeaderByNumber(ctx, test.GetMetadataWithFunds(), req)
		require.ErrorContains(t, err, "context canceled")
	})
}
