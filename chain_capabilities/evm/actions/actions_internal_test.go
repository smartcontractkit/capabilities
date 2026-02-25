package actions

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/contexts"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-framework/multinode"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions/mocks"
	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"
)

func TestNormalizeBlockNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                    string
		pbBlockNumber           *valuespb.BigInt
		expectedNumber          rpc.BlockNumber
		expectedIsLocking       bool
		expectedConfidenceLevel primitives.ConfidenceLevel
		expectedErrMsg          string
	}{
		{
			name:                    "nil block number",
			pbBlockNumber:           nil,
			expectedNumber:          rpc.LatestBlockNumber,
			expectedIsLocking:       true,
			expectedConfidenceLevel: primitives.Unconfirmed,
		},
		{
			name:              "non-int64 block number",
			pbBlockNumber:     valuespb.NewBigIntFromInt(big.NewInt(0).SetUint64(math.MaxUint64)), // Greater than max int64
			expectedNumber:    0,
			expectedIsLocking: false,
			expectedErrMsg:    "block number 18446744073709551615 is not an int64",
		},
		{
			name:                    "valid positive block number",
			pbBlockNumber:           valuespb.NewBigIntFromInt(big.NewInt(5)),
			expectedNumber:          5,
			expectedIsLocking:       false,
			expectedConfidenceLevel: primitives.Unconfirmed,
		},
		{
			name:                    "safe block number",
			pbBlockNumber:           valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.SafeBlockNumber))),
			expectedNumber:          rpc.SafeBlockNumber,
			expectedIsLocking:       true,
			expectedConfidenceLevel: primitives.Safe,
		},
		{
			name:                    "finalized block number",
			pbBlockNumber:           valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber))),
			expectedNumber:          rpc.FinalizedBlockNumber,
			expectedIsLocking:       true,
			expectedConfidenceLevel: primitives.Finalized,
		},
		{
			name:                    "latest block number",
			pbBlockNumber:           valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.LatestBlockNumber))),
			expectedNumber:          rpc.LatestBlockNumber,
			expectedIsLocking:       true,
			expectedConfidenceLevel: primitives.Unconfirmed,
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

			gotNumber, gotLocking, confidenceLevel, err := normalizeBlockNumber(tt.pbBlockNumber)
			if tt.expectedErrMsg != "" {
				require.ErrorContains(t, err, tt.expectedErrMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedNumber, gotNumber)
				require.Equal(t, tt.expectedIsLocking, gotLocking)
				require.Equal(t, tt.expectedConfidenceLevel, confidenceLevel)
			}
		})
	}
}

func TestReadType(t *testing.T) {
	t.Run("Handler returns error", func(t *testing.T) {
		reader := mocks.NewConsensusHandler(t)
		reader.EXPECT().Handle(mock.Anything, mock.Anything).Return(make(chan types.Reply), assert.AnError).Once()
		_, err := readType[int](t.Context(), reader, types.NewAggregatableRequest("id", nil))
		require.Equal(t, assert.AnError, err)
	})
	t.Run("Returns timeout", func(t *testing.T) {
		reader := mocks.NewConsensusHandler(t)
		reader.EXPECT().Handle(mock.Anything, mock.Anything).Return(make(chan types.Reply), nil).Once()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := readType[int](ctx, reader, types.NewAggregatableRequest("id", nil))
		require.Equal(t, context.Canceled, err)
	})
	t.Run("Returns error if it's present in reply", func(t *testing.T) {
		reader := mocks.NewConsensusHandler(t)
		ch := make(chan types.Reply, 1)
		ch <- types.Reply{Err: assert.AnError}
		reader.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()
		_, err := readType[int](t.Context(), reader, types.NewAggregatableRequest("id", nil))
		require.Equal(t, assert.AnError, err)
	})
	t.Run("Happy path", func(t *testing.T) {
		reader := mocks.NewConsensusHandler(t)
		ch := make(chan types.Reply, 1)
		const expectedResult = 16
		ch <- types.Reply{Value: expectedResult}
		reader.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()
		result, err := readType[int](t.Context(), reader, types.NewAggregatableRequest("id", nil))
		require.NoError(t, err)
		require.Equal(t, expectedResult, result)
	})
}

func TestIsRevertError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "execution reverted with reason",
			err:      fmt.Errorf("execution reverted: division by zero"),
			expected: true,
		},
		{
			name:     "wrapped RPC revert",
			err:      fmt.Errorf("RPC call failed: execution reverted"),
			expected: true,
		},
		{
			name:     "execution reverted bare",
			err:      fmt.Errorf("execution reverted"),
			expected: true,
		},
		{
			name:     "non-revert error",
			err:      fmt.Errorf("insufficient funds"),
			expected: false,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, isRevertError(tc.err))
		})
	}
}

func TestIsUserError(t *testing.T) {
	t.Parallel()

	evm := &EVM{}

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "revert error is user error",
			err:      fmt.Errorf("RPC call failed: execution reverted: division by zero"),
			expected: true,
		},
		{
			name:     "bare execution reverted is user error",
			err:      fmt.Errorf("execution reverted"),
			expected: true,
		},
		{
			name:     "context.DeadlineExceeded is system error",
			err:      context.DeadlineExceeded,
			expected: false,
		},
		{
			name:     "multinode.ErrNodeError is system error",
			err:      multinode.ErrNodeError,
			expected: false,
		},
		{
			name:     "generic error is user error",
			err:      fmt.Errorf("some other error"),
			expected: true,
		},
		{
			name:     "wrapped DeadlineExceeded is system error",
			err:      fmt.Errorf("operation failed: %w", context.DeadlineExceeded),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.expected, evm.isUserError(tc.err))
		})
	}
}

func TestFilterLogs(t *testing.T) {
	chainHeight := types.ChainHeight{Latest: 256, Safe: 128, Finalized: 64}
	testCases := []struct {
		Name                             string
		EthFilterQuery                   evmtypes.FilterQuery
		ExpectedFilterLogsToRequestError string
		ExpectedFilterLogsRequest        *evmtypes.FilterLogsRequest
		ExpectedCaptureObservationError  string
	}{
		{
			Name: "Block hash and block range both set",
			EthFilterQuery: evmtypes.FilterQuery{
				BlockHash: evmtypes.Hash{1, 2, 3},
				FromBlock: big.NewInt(10),
				ToBlock:   big.NewInt(20),
			},
			ExpectedFilterLogsToRequestError: "cannot specify both block hash and block range",
		},
		{
			Name: "Block hash set",
			EthFilterQuery: evmtypes.FilterQuery{
				BlockHash: evmtypes.Hash{1, 2, 3},
			},
			ExpectedFilterLogsRequest: &evmtypes.FilterLogsRequest{
				FilterQuery: evmtypes.FilterQuery{
					BlockHash: evmtypes.Hash{1, 2, 3},
				},
				ConfidenceLevel: primitives.Unconfirmed,
				IsExternal:      true,
			},
		},
		{
			Name: "FromBlock tag is not supported",
			EthFilterQuery: evmtypes.FilterQuery{
				FromBlock: big.NewInt(int64(rpc.EarliestBlockNumber)),
			},
			ExpectedFilterLogsToRequestError: "fromBlock is invalid: block number -5 is not supported",
		},
		{
			Name: "ToBlock tag is not supported",
			EthFilterQuery: evmtypes.FilterQuery{
				ToBlock: big.NewInt(int64(rpc.EarliestBlockNumber)),
			},
			ExpectedFilterLogsToRequestError: "toBlock is invalid: block number -5 is not supported",
		},
		{
			Name: "FromBlock > ToBlock",
			EthFilterQuery: evmtypes.FilterQuery{
				FromBlock: big.NewInt(20),
				ToBlock:   big.NewInt(10),
			},
			ExpectedFilterLogsToRequestError: "toBlock 10 is less than fromBlock 20",
		},
		{
			Name: "Eventually consistent block range too large",
			EthFilterQuery: evmtypes.FilterQuery{
				FromBlock: big.NewInt(1),
				ToBlock:   big.NewInt(102),
			},
			ExpectedFilterLogsToRequestError: "PerWorkflow.ChainRead.LogQueryBlockLimit limited for workflow[wf-id]: cannot use 101, limit is 100",
		},
		{
			Name: "Eventually consistent happy path",
			EthFilterQuery: evmtypes.FilterQuery{
				FromBlock: big.NewInt(1),
				ToBlock:   big.NewInt(101),
			},
			ExpectedFilterLogsRequest: &evmtypes.FilterLogsRequest{
				FilterQuery: evmtypes.FilterQuery{
					FromBlock: big.NewInt(1),
					ToBlock:   big.NewInt(101),
				},
				ConfidenceLevel: primitives.Unconfirmed,
				IsExternal:      true,
			},
		},
		{
			Name: "Lockable to a block: invalid range",
			EthFilterQuery: evmtypes.FilterQuery{
				FromBlock: big.NewInt(rpc.FinalizedBlockNumber.Int64()),
				ToBlock:   big.NewInt(rpc.LatestBlockNumber.Int64()),
			},
			ExpectedCaptureObservationError: "PerWorkflow.ChainRead.LogQueryBlockLimit limited for workflow[wf-id]: cannot use 192, limit is 100",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			svc := InitMocks(t)
			if tc.ExpectedFilterLogsRequest != nil {
				svc.EvmService.EXPECT().FilterLogs(mock.Anything, *tc.ExpectedFilterLogsRequest).Return(&evmtypes.FilterLogsReply{}, nil).Once()
			}
			ctx := contexts.WithCRE(t.Context(), contexts.CRE{Workflow: "wf-id"})
			request, err := svc.EVM.filterLogsToRequest(ctx, capabilities.RequestMetadata{}, tc.EthFilterQuery)
			if tc.ExpectedFilterLogsToRequestError != "" {
				require.ErrorContains(t, err, tc.ExpectedFilterLogsToRequestError)
				return
			}

			require.NoError(t, err)
			if lockable, ok := request.(*types.LockableToBlockRequest); ok {
				request = lockable.ToEventuallyConsistent(&chainHeight)
			}

			eventuallyConsistent := request.(*types.EventuallyConsistentRequest)
			err = eventuallyConsistent.CaptureObservation(ctx)
			if tc.ExpectedCaptureObservationError != "" {
				require.ErrorContains(t, err, tc.ExpectedCaptureObservationError)
				return
			}
			require.NoError(t, err)
		})
	}
}
