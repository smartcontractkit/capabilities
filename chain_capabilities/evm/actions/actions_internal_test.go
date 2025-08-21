package actions

import (
	"context"
	"math"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
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
