package actions

import (
	"context"
	"math"
	"math/big"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	chainsevm "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	chaincommonpb "github.com/smartcontractkit/chainlink-common/pkg/loop/chain-common"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"

	evmmock "github.com/smartcontractkit/chainlink-common/pkg/types/mocks"

	"github.com/smartcontractkit/chain_capabilities/evm/actions/mocks"
)

type evmWithMocks struct {
	EVM
	evmService      *evmmock.EVMService
	consensusReader *mocks.ConsensusReader
}

func initMocks(t *testing.T) *evmWithMocks {
	t.Helper()

	evmSvc := evmmock.NewEVMService(t)
	consensusReader := mocks.NewConsensusReader(t)
	return &evmWithMocks{
		EVM:             EVM{EVMService: evmSvc, consensusReader: consensusReader},
		evmService:      evmSvc,
		consensusReader: consensusReader,
	}
}

func TestNormalizeBlockNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		pbBlockNumber     *valuespb.BigInt
		expectedNumber    rpcBlockNumber
		expectedIsLocking bool
		expectedErrMsg    string
	}{
		{
			name:              "nil block number",
			pbBlockNumber:     nil,
			expectedNumber:    latestBlockNumber,
			expectedIsLocking: true,
		},
		{
			name:              "non-int64 block number",
			pbBlockNumber:     valuespb.NewBigIntFromInt(big.NewInt(0).SetUint64(math.MaxUint64)), // Greater than max int64
			expectedNumber:    0,
			expectedIsLocking: false,
			expectedErrMsg:    "block number 18446744073709551615 is not an int64",
		},
		{
			name:              "valid positive block number",
			pbBlockNumber:     valuespb.NewBigIntFromInt(big.NewInt(5)),
			expectedNumber:    5,
			expectedIsLocking: false,
		},
		{
			name:              "safe block number",
			pbBlockNumber:     valuespb.NewBigIntFromInt(big.NewInt(int64(safeBlockNumber))),
			expectedNumber:    safeBlockNumber,
			expectedIsLocking: true,
		},
		{
			name:              "finalized block number",
			pbBlockNumber:     valuespb.NewBigIntFromInt(big.NewInt(int64(finalizedBlockNumber))),
			expectedNumber:    finalizedBlockNumber,
			expectedIsLocking: true,
		},
		{
			name:              "latest block number",
			pbBlockNumber:     valuespb.NewBigIntFromInt(big.NewInt(int64(latestBlockNumber))),
			expectedNumber:    latestBlockNumber,
			expectedIsLocking: true,
		},
		{
			name:              "unsupported negative block number",
			pbBlockNumber:     valuespb.NewBigIntFromInt(big.NewInt(-99)),
			expectedNumber:    0,
			expectedIsLocking: false,
			expectedErrMsg:    "block number -99 is not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotNumber, gotLocking, err := normalizeBlockNumber(tt.pbBlockNumber)
			if tt.expectedErrMsg != "" {
				require.ErrorContains(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedNumber, gotNumber)
				require.Equal(t, tt.expectedIsLocking, gotLocking)
			}
		})
	}
}

func TestCapability_CallContract(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)
		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := chainsevm.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		ch := make(chan []byte, 1)
		ch <- []byte("ok")
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.CallContract(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Equal(t, []byte("ok"), resp.Data)
	})
	t.Run("On timeout returns error", func(t *testing.T) {
		svc := initMocks(t)
		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := chainsevm.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		ch := make(chan []byte, 1)
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
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
		ch := make(chan []byte, 1)
		balance, err := proto.Marshal(valuespb.NewBigIntFromInt(big.NewInt(1000)))
		require.NoError(t, err)
		ch <- balance
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.BalanceAtRequest{Account: []byte("by_account"), BlockNumber: valuespb.NewBigIntFromInt(block)}
		resp, err := svc.BalanceAt(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Equal(t, int64(1000), valuespb.NewIntFromBigInt(resp.Balance).Int64())
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		block := big.NewInt(123)
		ch := make(chan []byte, 1)
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.BalanceAtRequest{Account: []byte("by_account"), BlockNumber: valuespb.NewBigIntFromInt(block)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.BalanceAt(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_FilterLogs(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan []byte, 1)
		expectedReply := &chainsevm.FilterLogsReply{Logs: []*chainsevm.Log{{Address: []byte("0xabc"), Data: []byte("0xdef")}}}
		logs, err := proto.Marshal(expectedReply)
		require.NoError(t, err)
		ch <- logs
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.FilterLogsRequest{FilterQuery: &chainsevm.FilterQuery{BlockHash: make([]byte, 32)}}
		resp, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(expectedReply, resp, protocmp.Transform()))
	})
	t.Run("Returns error if both block hash and block range is used", func(t *testing.T) {
		svc := initMocks(t)
		req := &chainsevm.FilterLogsRequest{FilterQuery: &chainsevm.FilterQuery{BlockHash: make([]byte, 32), FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1))}}
		_, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "cannot specify both block hash and block range")
	})
	t.Run("Returns error if both block hash is of invalid length", func(t *testing.T) {
		svc := initMocks(t)
		req := &chainsevm.FilterLogsRequest{FilterQuery: &chainsevm.FilterQuery{BlockHash: make([]byte, 2)}}
		_, err := svc.FilterLogs(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan []byte, 1)
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.FilterLogsRequest{FilterQuery: &chainsevm.FilterQuery{}}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.FilterLogs(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_GetTransactionByHash(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan []byte, 1)
		tx := &chainsevm.Transaction{Nonce: 12}
		transaction, err := proto.Marshal(tx)
		require.NoError(t, err)
		ch <- transaction
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.GetTransactionByHashRequest{Hash: make([]byte, 32)}
		resp, err := svc.GetTransactionByHash(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(chainsevm.GetTransactionByHashReply{Transaction: tx}, resp, protocmp.Transform()))
	})
	t.Run("Returns error on invalid hash", func(t *testing.T) {
		svc := initMocks(t)

		req := &chainsevm.GetTransactionByHashRequest{Hash: make([]byte, 2)}
		_, err := svc.GetTransactionByHash(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan []byte, 1)
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.GetTransactionByHashRequest{Hash: make([]byte, 32)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.GetTransactionByHash(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_GetTransactionReceipt(t *testing.T) {
	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan []byte, 1)
		receipt := &chainsevm.Receipt{Status: 12}
		rawReceipt, err := proto.Marshal(receipt)
		require.NoError(t, err)
		ch <- rawReceipt
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.GetTransactionReceiptRequest{Hash: make([]byte, 32)}
		resp, err := svc.GetTransactionReceipt(t.Context(), capabilities.RequestMetadata{}, req)
		require.NoError(t, err)
		require.Empty(t, cmp.Diff(chainsevm.GetTransactionReceiptReply{Receipt: receipt}, resp, protocmp.Transform()))
	})
	t.Run("Returns error on invalid hash", func(t *testing.T) {
		svc := initMocks(t)

		req := &chainsevm.GetTransactionReceiptRequest{Hash: make([]byte, 2)}
		_, err := svc.GetTransactionReceipt(t.Context(), capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "invalid hash: got 2 bytes, expected 32")
	})
	t.Run("Returns error on timeout", func(t *testing.T) {
		svc := initMocks(t)

		ch := make(chan []byte, 1)
		svc.consensusReader.EXPECT().Read(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &chainsevm.GetTransactionReceiptRequest{Hash: make([]byte, 32)}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := svc.GetTransactionReceipt(ctx, capabilities.RequestMetadata{}, req)
		require.ErrorContains(t, err, "context canceled")
	})
}

func TestCapability_Register_Unregister_LogTracking(t *testing.T) {
	filterProto := &chainsevm.LPFilter{} // empty is enough for proto→types conversion

	t.Run("register happy-path", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(nil)

		_, err := svc.RegisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&chainsevm.RegisterLogTrackingRequest{Filter: filterProto})
		require.NoError(t, err)
	})

	t.Run("register error", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("RegisterLogTracking", mock.Anything, mock.Anything).Return(assert.AnError)

		_, err := svc.RegisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&chainsevm.RegisterLogTrackingRequest{Filter: filterProto})
		assert.ErrorIs(t, err, assert.AnError)
	})

	t.Run("unregister happy-path", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("UnregisterLogTracking", mock.Anything, "myFilter").Return(nil)

		_, err := svc.UnregisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&chainsevm.UnregisterLogTrackingRequest{FilterName: "myFilter"})
		require.NoError(t, err)
	})

	t.Run("unregister error", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("UnregisterLogTracking", mock.Anything, "myFilter").Return(assert.AnError)

		_, err := svc.UnregisterLogTracking(context.Background(), capabilities.RequestMetadata{},
			&chainsevm.UnregisterLogTrackingRequest{FilterName: "myFilter"})
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestCapability_QueryTrackedLogs(t *testing.T) {
	t.Parallel()

	expLimitAndSort := query.NewLimitAndSort(query.CountLimit(10), query.NewSortByTimestamp(query.Asc))
	expConfidence := primitives.Finalized
	expLogs := []*evmtypes.Log{{LogIndex: 2, Address: evmtypes.Address{1}}}

	lsProto, _ := chaincommonpb.ConvertLimitAndSortToProto(expLimitAndSort)

	simpleExpr := []query.Expression{
		query.TxHash("0xabcdeffeedfacecafebeef0123456789abcdef0123456789abcdef01234567"),
	}
	exprProto, _ := chainsevm.ConvertExpressionsToProto(simpleExpr)

	req := &chainsevm.QueryTrackedLogsRequest{
		Expression:      exprProto,
		LimitAndSort:    lsProto,
		ConfidenceLevel: chaincommonpb.Confidence_Finalized,
	}

	t.Run("happy-path", func(t *testing.T) {
		svc := initMocks(t)
		svc.evmService.On("QueryTrackedLogs",
			mock.Anything,
			simpleExpr,
			expLimitAndSort,
			expConfidence,
		).Return(expLogs, nil).Once()

		resp, err := svc.QueryTrackedLogs(
			context.Background(), capabilities.RequestMetadata{}, req,
		)
		require.NoError(t, err)
		require.Len(t, resp.Logs, 1)
	})

	t.Run("EVM error bubbles", func(t *testing.T) {
		svc := initMocks(t)

		svc.evmService.On("QueryTrackedLogs",
			mock.Anything, mock.Anything, expLimitAndSort, expConfidence,
		).Return(nil, assert.AnError).Once()

		_, err := svc.QueryTrackedLogs(
			context.Background(), capabilities.RequestMetadata{}, req,
		)
		assert.ErrorIs(t, err, assert.AnError)
	})
}
