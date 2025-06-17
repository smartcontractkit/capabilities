package oracle

import (
	"math/big"
	"testing"

	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"github.com/stretchr/testify/require"
)

func TestMaxProtoBigInt(t *testing.T) {
	testCases := []struct {
		Name string
		In   []int64
		Out  *pb.BigInt
	}{
		{
			Name: "Empty",
		},
		{
			Name: "Single",
			In:   []int64{1},
			Out:  pbBigFromInt(1),
		},
		{
			Name: "Multiple",
			In:   []int64{1, 3, 2, 0},
			Out:  pbBigFromInt(3),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			in := make([]*pb.BigInt, len(tc.In))
			for i := range tc.In {
				in[i] = pbBigFromInt(tc.In[i])
			}

			result := maxProtoBigInt(in...)
			require.Equal(t, tc.Out, result)
		})
	}
}

func TestEnsureGtOrEq(t *testing.T) {
	require.NoError(t, ensureGtOrEq(pbBigFromInt(1), pbBigFromInt(1)))
	require.NoError(t, ensureGtOrEq(pbBigFromInt(10), pbBigFromInt(1)))
	require.EqualError(t, ensureGtOrEq(pbBigFromInt(1000), pbBigFromInt(20000)), "expected 1000 to be >= 20000")
}

func pbBigFromInt(i int64) *pb.BigInt {
	return pb.NewBigIntFromInt(big.NewInt(i))
}

func TestValidateBlockHeight(t *testing.T) {
	tests := []struct {
		name        string
		observation *evmservice.Observations
		expectedErr string
	}{
		{
			name: "Valid heights",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(10),
				Safe:      pbBigFromInt(8),
				Finalized: pbBigFromInt(5),
			},
			expectedErr: "",
		},
		{
			name: "All equal",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(10),
				Safe:      pbBigFromInt(10),
				Finalized: pbBigFromInt(10),
			},
			expectedErr: "",
		},
		{
			name: "Latest less than Safe",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(7),
				Safe:      pbBigFromInt(8),
				Finalized: pbBigFromInt(5),
			},
			expectedErr: "expected latest to be gtOrEq to safe: expected 7 to be >= 8",
		},
		{
			name: "Safe less than Finalized",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(10),
				Safe:      pbBigFromInt(4),
				Finalized: pbBigFromInt(5),
			},
			expectedErr: "expected safe to be gtOrEq to finalized: expected 4 to be >= 5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBlockHeight(tc.observation)
			if tc.expectedErr != "" {
				require.EqualError(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFPlusOneLowestBlockHeight(t *testing.T) {
	tests := []struct {
		name        string
		obs         []evmservice.Observations
		f           int
		getHeight   func(ob *evmservice.Observations) *pb.BigInt
		expected    *pb.BigInt
		expectedErr string
	}{
		{
			name: "valid observations, f = 1",
			obs: []evmservice.Observations{
				{Latest: pbBigFromInt(10)},
				{Latest: pbBigFromInt(20)},
				{Latest: pbBigFromInt(15)},
			},
			f: 1,
			getHeight: func(ob *evmservice.Observations) *pb.BigInt {
				return ob.Latest
			},
			expected: pbBigFromInt(15),
		},
		{
			name: "not enough observations",
			obs: []evmservice.Observations{
				{Latest: pbBigFromInt(10)},
			},
			f: 1,
			getHeight: func(ob *evmservice.Observations) *pb.BigInt {
				return ob.Latest
			},
			expected:    nil,
			expectedErr: "not enough observations to calculate F+1 lowest block height. Got 1, expected at least 2",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := fPlusOneLowestBlockHeight(tc.obs, tc.f, tc.getHeight)
			if tc.expectedErr != "" {
				require.EqualError(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expected, result)
			}
		})
	}
}

func TestValidateBlockHeightAgainstOutcome(t *testing.T) {
	tests := []struct {
		name        string
		observation *evmservice.Observations
		previous    *evmservice.Outcome
		expectError string
	}{
		{
			name: "Happy path",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(100),
				Safe:      pbBigFromInt(90),
				Finalized: pbBigFromInt(80),
			},
			previous: &evmservice.Outcome{
				Latest:    pbBigFromInt(99),
				Safe:      pbBigFromInt(89),
				Finalized: pbBigFromInt(79),
			},
		},
		{
			name: "All equal",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(80),
				Safe:      pbBigFromInt(80),
				Finalized: pbBigFromInt(80),
			},
			previous: &evmservice.Outcome{
				Latest:    pbBigFromInt(80),
				Safe:      pbBigFromInt(80),
				Finalized: pbBigFromInt(80),
			},
		},
		{
			name: "latest block lower than previous",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(98),
				Safe:      pbBigFromInt(90),
				Finalized: pbBigFromInt(80),
			},
			previous: &evmservice.Outcome{
				Latest:    pbBigFromInt(99),
				Safe:      pbBigFromInt(89),
				Finalized: pbBigFromInt(79),
			},
			expectError: "expected latest to be gtOrEq to previous latest: expected 98 to be >= 99",
		},
		{
			name: "safe block lower than previous",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(100),
				Safe:      pbBigFromInt(88),
				Finalized: pbBigFromInt(80),
			},
			previous: &evmservice.Outcome{
				Latest:    pbBigFromInt(99),
				Safe:      pbBigFromInt(89),
				Finalized: pbBigFromInt(79),
			},
			expectError: "expected safe to be gtOrEq to previous safe: expected 88 to be >= 89",
		},
		{
			name: "finalized block lower than previous",
			observation: &evmservice.Observations{
				Latest:    pbBigFromInt(100),
				Safe:      pbBigFromInt(90),
				Finalized: pbBigFromInt(78),
			},
			previous: &evmservice.Outcome{
				Latest:    pbBigFromInt(99),
				Safe:      pbBigFromInt(89),
				Finalized: pbBigFromInt(79),
			},
			expectError: "expected finalized to be gtOrEq to previous finalized: expected 78 to be >= 79",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBlockHeightAgainstOutcome(tt.observation, tt.previous)
			if tt.expectError != "" {
				require.EqualError(t, err, tt.expectError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
