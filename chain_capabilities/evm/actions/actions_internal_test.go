package actions

import (
	"math"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/stretchr/testify/require"
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
