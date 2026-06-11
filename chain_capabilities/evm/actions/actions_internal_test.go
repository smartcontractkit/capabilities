package actions

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/rpc"
	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/settings/limits"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmprotos "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"
	"github.com/smartcontractkit/chainlink-framework/multinode"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

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
			name: "grpc-style node error without ErrNodeError wrap is system error",
			err: fmt.Errorf(
				"rpc error: code = Unknown desc = %s",
				multinode.ErrNodeError.Error(),
			),
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
		Name                      string
		EthFilterQuery            *evmprotos.FilterQuery
		ExpectedError             string
		ExpectedFilterLogsRequest *evmtypes.FilterLogsRequest
		ExpectedResponse          *evmtypes.FilterLogsReply
	}{
		{
			Name: "Block hash and block range both set",
			EthFilterQuery: &evmprotos.FilterQuery{
				BlockHash: append([]byte{1, 2, 3}, make([]byte, 29)...),
				FromBlock: valuespb.NewBigIntFromInt(big.NewInt(10)),
				ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(20)),
			},
			ExpectedError: "cannot specify both block hash and block range",
		},
		{
			Name: "Block hash set",
			EthFilterQuery: &evmprotos.FilterQuery{
				BlockHash: append([]byte{1, 2, 3}, make([]byte, 29)...),
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
			EthFilterQuery: &evmprotos.FilterQuery{
				FromBlock: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.EarliestBlockNumber))),
			},
			ExpectedError: "fromBlock is invalid: block number -5 is not supported",
		},
		{
			Name: "ToBlock tag is not supported",
			EthFilterQuery: &evmprotos.FilterQuery{
				ToBlock: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.EarliestBlockNumber))),
			},
			ExpectedError: "toBlock is invalid: block number -5 is not supported",
		},
		{
			Name: "FromBlock > ToBlock",
			EthFilterQuery: &evmprotos.FilterQuery{
				FromBlock: valuespb.NewBigIntFromInt(big.NewInt(20)),
				ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(10)),
			},
			ExpectedError: "toBlock 10 is less than fromBlock 20",
		},
		{
			Name: "Eventually consistent block range too large",
			EthFilterQuery: &evmprotos.FilterQuery{
				FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
				ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(102)),
			},
			ExpectedError: "PerWorkflow.ChainRead.LogQueryBlockLimit limited for workflow[wf-id]: cannot use 101, limit is 100",
		},
		{
			Name: "Eventually consistent happy path",
			EthFilterQuery: &evmprotos.FilterQuery{
				FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
				ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(101)),
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
			EthFilterQuery: &evmprotos.FilterQuery{
				FromBlock: valuespb.NewBigIntFromInt(big.NewInt(rpc.FinalizedBlockNumber.Int64())),
				ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(rpc.LatestBlockNumber.Int64())),
			},
			ExpectedError: "PerWorkflow.ChainRead.LogQueryBlockLimit limited for workflow[wf-id]: cannot use 192, limit is 100",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			svc := InitMocks(t)
			if tc.ExpectedFilterLogsRequest != nil {
				if len(tc.ExpectedFilterLogsRequest.FilterQuery.Topics) == 0 {
					tc.ExpectedFilterLogsRequest.FilterQuery.Topics = make([][]evmtypes.Hash, 0) // to avoid nil vs empty slice issues in mock expectations
					tc.ExpectedFilterLogsRequest.FilterQuery.Addresses = make([]evmtypes.Address, 0)
				}
				svc.EvmService.EXPECT().FilterLogs(mock.Anything, *tc.ExpectedFilterLogsRequest).Return(&evmtypes.FilterLogsReply{}, nil).Once()
			}
			svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, request types.Request) (<-chan types.Reply, error) {
				if lockable, ok := request.(*types.LockableToBlockRequest); ok {
					request = lockable.LockToABlock(&chainHeight)
				}

				eventuallyConsistent := request.(*types.EventuallyConsistentRequest)
				result := make(chan types.Reply, 1)
				err := eventuallyConsistent.CaptureObservation(ctx)
				if err != nil {
					result <- types.Reply{Err: err}
					close(result)
					return result, nil
				}
				ob, _, ok := eventuallyConsistent.GetObservation()
				require.True(t, ok)
				result <- types.Reply{Value: ob}
				close(result)
				return result, nil
			}).Maybe()
			metadata := capabilities.RequestMetadata{WorkflowID: "wf-id"}
			ctx := metadata.ContextWithCRE(t.Context())
			_, err := svc.FilterLogs(ctx, metadata, &evmprotos.FilterLogsRequest{FilterQuery: tc.EthFilterQuery})
			if tc.ExpectedError != "" {
				require.ErrorContains(t, err, tc.ExpectedError)
				return
			}
		})
	}
}

func TestEVMEnsureCapabilityError(t *testing.T) {
	evm := &EVM{lggr: logger.Sugared(logger.Test(t))}
	limitErr := limits.NewUpperBoundLimiter[uint64](10).Check(t.Context(), 11)
	require.Error(t, limitErr)

	publicUserErr := caperrors.NewPublicUserError(errors.New("invalid receipt hash"), caperrors.InvalidArgument)
	publicSystemErr := caperrors.NewPublicSystemError(errors.New("failed to marshal receipt reply"), caperrors.Internal)

	testCases := []struct {
		name           string
		err            error
		expectedOrigin caperrors.Origin
		expectedCode   caperrors.ErrorCode
		expectedError  string
	}{
		{
			name:           "preserves capability user error",
			err:            publicUserErr,
			expectedOrigin: caperrors.OriginUser,
			expectedCode:   caperrors.InvalidArgument,
			expectedError:  publicUserErr.Error(),
		},
		{
			name:           "preserves capability system error",
			err:            publicSystemErr,
			expectedOrigin: caperrors.OriginSystem,
			expectedCode:   caperrors.Internal,
			expectedError:  publicSystemErr.Error(),
		},
		{
			name:           "wraps known user limit error",
			err:            limitErr,
			expectedOrigin: caperrors.OriginUser,
			expectedCode:   caperrors.LimitExceeded,
			expectedError:  "limited: cannot use 11, limit is 10",
		},
		{
			name:           "wraps known system timeout error",
			err:            context.DeadlineExceeded,
			expectedOrigin: caperrors.OriginSystem,
			expectedCode:   caperrors.Unknown,
			expectedError:  context.DeadlineExceeded.Error(),
		},
		{
			name:           "wraps node infra error matched by errors.Is",
			err:            fmt.Errorf("rpc call failed: %w", multinode.ErrNodeError),
			expectedOrigin: caperrors.OriginSystem,
			expectedCode:   caperrors.Unknown,
			expectedError:  multinode.ErrNodeError.Error(),
		},
		{
			name:           "wraps node infra error matched by string contains",
			err:            errors.New("rpc call failed: " + multinode.ErrNodeError.Error()),
			expectedOrigin: caperrors.OriginSystem,
			expectedCode:   caperrors.Unknown,
			expectedError:  multinode.ErrNodeError.Error(),
		},
		{
			name:           "wraps unknown plain error as user error",
			err:            errors.New("some unexpected failure"),
			expectedOrigin: caperrors.OriginUser,
			expectedCode:   caperrors.Unknown,
			expectedError:  "some unexpected failure",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := evm.ensureCapabilityError(tc.err)
			require.Equal(t, caperrors.VisibilityPublic, got.Visibility())
			require.Equal(t, tc.expectedOrigin, got.Origin())
			require.Equal(t, tc.expectedCode, got.Code())
			require.Contains(t, got.Error(), tc.expectedError)
		})
	}
}
