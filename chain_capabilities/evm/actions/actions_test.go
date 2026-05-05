package actions_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/metering"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"

	caperrors "github.com/smartcontractkit/chainlink-common/pkg/capabilities/errors"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"

	"github.com/smartcontractkit/capabilities/libs/chainconsensus/types"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"

	"google.golang.org/protobuf/testing/protocmp"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

func TestCapability_CallContract(t *testing.T) {
	happyPath := func(t *testing.T, isV2 bool) {
		chainHeight := &types.ChainHeight{
			Latest:    32,
			Safe:      16,
			Finalized: 8,
		}

		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, err := evmcappb.ConvertCallMsgToProto(&msg)
		require.NoError(t, err)
		expectedMsg, err := evmcappb.ConvertCallMsgFromProto(msgProto)
		require.NoError(t, err)

		testCases := []struct {
			Name               string
			Request            *evmcappb.CallContractRequest
			EvmServiceResponse *evmtypes.CallContractReply
			EvmServiceErr      error

			ExpectedEvmServiceRequest evmtypes.CallContractRequest
			ExpectedResponse          *evmcappb.CallContractReply
			ExpectedError             string
		}{
			{
				Name:               "call at latest finalized block returns data",
				Request:            &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber)))},
				EvmServiceResponse: &evmtypes.CallContractReply{Data: []byte{0x01, 0x02}},
				ExpectedEvmServiceRequest: evmtypes.CallContractRequest{
					Msg:             expectedMsg,
					BlockNumber:     big.NewInt(chainHeight.Finalized),
					ConfidenceLevel: primitives.Finalized,
					IsExternal:      true,
				},
				ExpectedResponse: &evmcappb.CallContractReply{Data: []byte{0x01, 0x02}},
			},
			{
				Name:          "call at latest finalized block returns error",
				Request:       &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber)))},
				EvmServiceErr: errors.New("finalized head call unavailable"),
				ExpectedEvmServiceRequest: evmtypes.CallContractRequest{
					Msg:             expectedMsg,
					BlockNumber:     big.NewInt(chainHeight.Finalized),
					ConfidenceLevel: primitives.Finalized,
					IsExternal:      true,
				},
				ExpectedError: "finalized head call unavailable",
			},
			{
				Name:               "call at fixed block number returns success",
				Request:            &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(5))},
				EvmServiceResponse: &evmtypes.CallContractReply{Data: []byte{0x03, 0x04}},
				ExpectedEvmServiceRequest: evmtypes.CallContractRequest{
					Msg:             expectedMsg,
					BlockNumber:     big.NewInt(5),
					ConfidenceLevel: primitives.Unconfirmed,
					IsExternal:      true,
				},
				ExpectedResponse: &evmcappb.CallContractReply{Data: []byte{0x03, 0x04}},
			},
			{
				Name:          "call at fixed block number returns error",
				Request:       &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(5))},
				EvmServiceErr: errors.New("missing trie node"),
				ExpectedEvmServiceRequest: evmtypes.CallContractRequest{
					Msg:             expectedMsg,
					BlockNumber:     big.NewInt(5),
					ConfidenceLevel: primitives.Unconfirmed,
					IsExternal:      true,
				},
				ExpectedError: "missing trie node",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.Name, func(t *testing.T) {
				svc := actions.InitMocks(t)
				svc.EvmService.EXPECT().CallContract(mock.Anything, tc.ExpectedEvmServiceRequest).Return(tc.EvmServiceResponse, tc.EvmServiceErr).Once()

				svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, request types.Request) (<-chan types.Reply, error) {
					return runAndReturnHandle(ctx, request, chainHeight)
				}).Once()

				meta := test.GetMetadataWithFunds()
				if isV2 {
					meta = metaWithHashBasedConsensus(meta)
				}
				ctx := meta.ContextWithCRE(t.Context())
				resp, err := svc.CallContract(ctx, meta, tc.Request)
				if tc.ExpectedError != "" {
					require.Error(t, err)
					require.ErrorContains(t, err, tc.ExpectedError)
					return
				}
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(tc.ExpectedResponse, resp.Response, protocmp.Transform()))
				test.ValidateMetering(t, resp.ResponseMetadata, string(metering.CallContract))
				if isV2 {
					require.NotNil(t, resp.OCRAttestation)
				} else {
					require.Nil(t, resp.OCRAttestation)
				}
			})
		}
	}

	t.Run("Happy path V1", func(t *testing.T) {
		happyPath(t, false)
	})
	t.Run("Happy path V2", func(t *testing.T) {
		happyPath(t, true)
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

	t.Run("revert error is classified as user error", func(t *testing.T) {
		svc := actions.InitMocks(t)
		msg := evmtypes.CallMsg{Data: []byte{0xbe, 0xef}}
		msgProto, _ := evmcappb.ConvertCallMsgToProto(&msg)

		block := big.NewInt(123)
		ch := make(chan types.Reply, 1)
		ch <- types.Reply{Err: fmt.Errorf("RPC call failed: execution reverted: division by zero")}
		svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).Return(ch, nil).Once()

		req := &evmcappb.CallContractRequest{Call: msgProto, BlockNumber: valuespb.NewBigIntFromInt(block)}
		_, err := svc.CallContract(t.Context(), test.GetMetadataWithFunds(), req)
		require.Error(t, err)
		require.ErrorContains(t, err, "execution reverted")
		var capErr caperrors.Error
		require.True(t, errors.As(err, &capErr), "error should be a caperrors.Error")
		require.Equal(t, caperrors.OriginUser, capErr.Origin(), "revert error should be classified as user error")
	})
}

func runAndReturnHandle(ctx context.Context, req types.Request, chainHeight *types.ChainHeight) (<-chan types.Reply, error) {
	// if request is lockable - lock it to simulate the behavior of the consensus handler and then capture observation
	lockable, ok := req.(interface {
		LockToABlock(chainHeight *types.ChainHeight) types.Request
	})
	if ok {
		req = lockable.LockToABlock(chainHeight)
	}

	observableRequest, ok := req.(types.ObservableRequest)
	if !ok {
		return nil, fmt.Errorf("request is not an ObservableRequest")
	}
	// RPC observed errors are returned from CaptureObservation but are still encoded in the GetOCRObservation, so we should just ignore the error
	_ = observableRequest.CaptureObservation(ctx)
	observation, err := observableRequest.GetOCRObservation()
	if err != nil {
		return nil, fmt.Errorf("failed to get OCR observation: %w", err)
	}

	var reply types.Reply
	switch tObservation := observation.Observation.(type) {
	case *types.RequestObservation_Hashable:
		if len(tObservation.Hashable) != types.HashLength {
			return nil, fmt.Errorf("unexpected hashable observation length: got %d, want %d", len(tObservation.Hashable), types.HashLength)
		}
		var rd [types.HashLength]byte
		copy(rd[:], tObservation.Hashable)
		reply = types.Reply{Value: types.NewHashableRequestReport(ocrtypes.ConfigDigest{}, 0, rd, nil)}
	case *types.RequestObservation_Error:
		obsErr := types.ObservationError(tObservation.Error)
		replyErr := obsErr.Err()
		if replyErr == nil {
			return nil, fmt.Errorf("unexpected nil error in observation error")
		}
		reply = types.Reply{Err: replyErr}
	case *types.RequestObservation_EventuallyConsistent:
		reply = types.Reply{Value: tObservation.EventuallyConsistent}
	default:
		return nil, fmt.Errorf("unexpected observation type: %T", observation.Observation)
	}

	ch := make(chan types.Reply, 1)
	ch <- reply
	return ch, nil
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
	happyPath := func(t *testing.T, isV2 bool) {
		chainHeight := &types.ChainHeight{
			Latest:    32,
			Safe:      16,
			Finalized: 8,
		}

		account := common.HexToAddress("0x1")
		addr, err := evmservice.ConvertOptionalAddressFromProto(account[:])
		require.NoError(t, err)

		testCases := []struct {
			Name               string
			Request            *evmcappb.BalanceAtRequest
			EvmServiceResponse *evmtypes.BalanceAtReply
			EvmServiceErr      error

			ExpectedEvmServiceRequest evmtypes.BalanceAtRequest
			ExpectedResponse          *evmcappb.BalanceAtReply
			ExpectedError             string
		}{
			{
				Name:               "call at finalized block returns balance",
				Request:            &evmcappb.BalanceAtRequest{Account: account[:], BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber)))},
				EvmServiceResponse: &evmtypes.BalanceAtReply{Balance: big.NewInt(1000)},
				ExpectedEvmServiceRequest: evmtypes.BalanceAtRequest{
					Address:         addr,
					BlockNumber:     big.NewInt(chainHeight.Finalized),
					ConfidenceLevel: primitives.Finalized,
				},
				ExpectedResponse: &evmcappb.BalanceAtReply{Balance: valuespb.NewBigIntFromInt(big.NewInt(1000))},
			},
			{
				Name:          "call at latest finalized block returns error",
				Request:       &evmcappb.BalanceAtRequest{Account: account[:], BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber)))},
				EvmServiceErr: errors.New("finalized head balance unavailable"),
				ExpectedEvmServiceRequest: evmtypes.BalanceAtRequest{
					Address:         addr,
					BlockNumber:     big.NewInt(chainHeight.Finalized),
					ConfidenceLevel: primitives.Finalized,
				},
				ExpectedError: "finalized head balance unavailable",
			},
			{
				Name:               "call at fixed block number returns success",
				Request:            &evmcappb.BalanceAtRequest{Account: account[:], BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(5))},
				EvmServiceResponse: &evmtypes.BalanceAtReply{Balance: big.NewInt(2000)},
				ExpectedEvmServiceRequest: evmtypes.BalanceAtRequest{
					Address:         addr,
					BlockNumber:     big.NewInt(5),
					ConfidenceLevel: primitives.Unconfirmed,
				},
				ExpectedResponse: &evmcappb.BalanceAtReply{Balance: valuespb.NewBigIntFromInt(big.NewInt(2000))},
			},
			{
				Name:          "call at fixed block number returns error",
				Request:       &evmcappb.BalanceAtRequest{Account: account[:], BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(5))},
				EvmServiceErr: errors.New("missing trie node"),
				ExpectedEvmServiceRequest: evmtypes.BalanceAtRequest{
					Address:         addr,
					BlockNumber:     big.NewInt(5),
					ConfidenceLevel: primitives.Unconfirmed,
				},
				ExpectedError: "missing trie node",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.Name, func(t *testing.T) {
				svc := actions.InitMocks(t)
				svc.EvmService.EXPECT().BalanceAt(mock.Anything, tc.ExpectedEvmServiceRequest).Return(tc.EvmServiceResponse, tc.EvmServiceErr).Once()

				svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, request types.Request) (<-chan types.Reply, error) {
					return runAndReturnHandle(ctx, request, chainHeight)
				}).Once()

				meta := test.GetMetadataWithFunds()
				if isV2 {
					meta = metaWithHashBasedConsensus(meta)
				}
				ctx := meta.ContextWithCRE(t.Context())
				resp, err := svc.BalanceAt(ctx, meta, tc.Request)
				if tc.ExpectedError != "" {
					require.Error(t, err)
					require.ErrorContains(t, err, tc.ExpectedError)
					return
				}
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(tc.ExpectedResponse, resp.Response, protocmp.Transform()))
				test.ValidateMetering(t, resp.ResponseMetadata, string(metering.BalanceAt))
				if isV2 {
					require.NotNil(t, resp.OCRAttestation)
				} else {
					require.Nil(t, resp.OCRAttestation)
				}
			})
		}
	}
	t.Run("Happy path V1", func(t *testing.T) {
		happyPath(t, false)
	})
	t.Run("Happy path V2", func(t *testing.T) {
		happyPath(t, true)
	})
}

func TestCapability_FilterLogs(t *testing.T) {
	happyPath := func(t *testing.T, isV2 bool) {
		defaultChainHeight := &types.ChainHeight{Latest: 32, Safe: 16, Finalized: 8}
		wideChainHeight := &types.ChainHeight{Latest: 132, Safe: 66, Finalized: 8}

		minLog := &evmtypes.Log{BlockNumber: big.NewInt(0), Data: []byte{0xab}}
		singleLogSvc := &evmtypes.FilterLogsReply{Logs: []*evmtypes.Log{minLog}}
		protoLogs, err := evmcappb.ConvertLogsToProto(singleLogSvc.Logs)
		require.NoError(t, err)
		expectedSingleProto := &evmcappb.FilterLogsReply{Logs: protoLogs}

		oversizedLog := &evmtypes.Log{
			BlockNumber: big.NewInt(0),
			Data:        bytes.Repeat([]byte{'z'}, 6000),
		}

		testCases := []struct {
			Name                      string
			Request                   *evmcappb.FilterLogsRequest
			EvmServiceResponse        *evmtypes.FilterLogsReply
			EvmServiceErr             error
			ExpectedEvmServiceRequest evmtypes.FilterLogsRequest
			ExpectedResponse          *evmcappb.FilterLogsReply
			ExpectedError             string
			ExpectConsensus           bool
			ExpectEvm                 bool
			ChainHeight               *types.ChainHeight
		}{
			{
				Name:               "block hash filter returns logs",
				Request:            &evmcappb.FilterLogsRequest{FilterQuery: &evmcappb.FilterQuery{BlockHash: bytes.Repeat([]byte{1}, 32), Topics: []*evmcappb.Topics{}}},
				EvmServiceResponse: singleLogSvc,
				ExpectedEvmServiceRequest: normalizeFilterLogsServiceRequest(evmtypes.FilterLogsRequest{
					FilterQuery:     evmtypes.FilterQuery{BlockHash: evmtypes.Hash(bytes.Repeat([]byte{1}, 32))},
					ConfidenceLevel: primitives.Unconfirmed,
					IsExternal:      true,
				}),
				ExpectedResponse: expectedSingleProto,
				ExpectConsensus:  true,
				ExpectEvm:        true,
			},
			{
				Name:          "block hash filter returns error",
				Request:       &evmcappb.FilterLogsRequest{FilterQuery: &evmcappb.FilterQuery{BlockHash: bytes.Repeat([]byte{1}, 32), Topics: []*evmcappb.Topics{}}},
				EvmServiceErr: errors.New("logs for block hash unavailable"),
				ExpectedEvmServiceRequest: normalizeFilterLogsServiceRequest(evmtypes.FilterLogsRequest{
					FilterQuery:     evmtypes.FilterQuery{BlockHash: evmtypes.Hash(bytes.Repeat([]byte{1}, 32))},
					ConfidenceLevel: primitives.Unconfirmed,
					IsExternal:      true,
				}),
				ExpectedError:   "logs for block hash unavailable",
				ExpectConsensus: true,
				ExpectEvm:       true,
			},
			{
				Name: "fixed block range returns success",
				Request: &evmcappb.FilterLogsRequest{
					FilterQuery: &evmcappb.FilterQuery{
						FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
						ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(101)),
						Topics:    []*evmcappb.Topics{},
					},
				},
				EvmServiceResponse: singleLogSvc,
				ExpectedEvmServiceRequest: normalizeFilterLogsServiceRequest(evmtypes.FilterLogsRequest{
					FilterQuery: evmtypes.FilterQuery{
						FromBlock: big.NewInt(1),
						ToBlock:   big.NewInt(101),
					},
					ConfidenceLevel: primitives.Unconfirmed,
					IsExternal:      true,
				}),
				ExpectedResponse: expectedSingleProto,
				ExpectConsensus:  true,
				ExpectEvm:        true,
			},
			{
				Name: "fixed block range returns evm error",
				Request: &evmcappb.FilterLogsRequest{
					FilterQuery: &evmcappb.FilterQuery{
						FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
						ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(101)),
						Topics:    []*evmcappb.Topics{},
					},
				},
				EvmServiceErr: errors.New("missing trie node"),
				ExpectedEvmServiceRequest: normalizeFilterLogsServiceRequest(evmtypes.FilterLogsRequest{
					FilterQuery: evmtypes.FilterQuery{
						FromBlock: big.NewInt(1),
						ToBlock:   big.NewInt(101),
					},
					ConfidenceLevel: primitives.Unconfirmed,
					IsExternal:      true,
				}),
				ExpectedError:   "missing trie node",
				ExpectConsensus: true,
				ExpectEvm:       true,
			},
			{
				Name: "fixed to finalized returns success",
				Request: &evmcappb.FilterLogsRequest{
					FilterQuery: &evmcappb.FilterQuery{
						FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
						ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber))),
						Topics:    []*evmcappb.Topics{},
					},
				},
				EvmServiceResponse: singleLogSvc,
				ExpectedEvmServiceRequest: normalizeFilterLogsServiceRequest(evmtypes.FilterLogsRequest{
					FilterQuery: evmtypes.FilterQuery{
						FromBlock: big.NewInt(1),
						ToBlock:   big.NewInt(8),
					},
					ConfidenceLevel: primitives.Finalized,
					IsExternal:      true,
				}),
				ExpectedResponse: expectedSingleProto,
				ExpectConsensus:  true,
				ExpectEvm:        true,
			},
			{
				Name: "fixed to finalized returns evm error",
				Request: &evmcappb.FilterLogsRequest{
					FilterQuery: &evmcappb.FilterQuery{
						FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
						ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber))),
						Topics:    []*evmcappb.Topics{},
					},
				},
				EvmServiceErr: errors.New("finalized head logs unavailable"),
				ExpectedEvmServiceRequest: normalizeFilterLogsServiceRequest(evmtypes.FilterLogsRequest{
					FilterQuery: evmtypes.FilterQuery{
						FromBlock: big.NewInt(1),
						ToBlock:   big.NewInt(8),
					},
					ConfidenceLevel: primitives.Finalized,
					IsExternal:      true,
				}),
				ExpectedError:   "finalized head logs unavailable",
				ExpectConsensus: true,
				ExpectEvm:       true,
			},
			{
				Name: "eventually consistent block range exceeds limit",
				Request: &evmcappb.FilterLogsRequest{
					FilterQuery: &evmcappb.FilterQuery{
						FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
						ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(102)),
						Topics:    []*evmcappb.Topics{},
					},
				},
				ExpectedError:   "LogQueryBlockLimit",
				ExpectConsensus: false,
				ExpectEvm:       false,
			},
			{
				Name: "lockable block range exceeds limit after resolving tags",
				Request: &evmcappb.FilterLogsRequest{
					FilterQuery: &evmcappb.FilterQuery{
						FromBlock: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber))),
						ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.LatestBlockNumber))),
						Topics:    []*evmcappb.Topics{},
					},
				},
				ExpectedError:   "cannot use 124",
				ExpectConsensus: true,
				ExpectEvm:       false,
				ChainHeight:     wideChainHeight,
			},
			{
				Name: "response payload exceeds configured size limit",
				Request: &evmcappb.FilterLogsRequest{
					FilterQuery: &evmcappb.FilterQuery{
						FromBlock: valuespb.NewBigIntFromInt(big.NewInt(1)),
						ToBlock:   valuespb.NewBigIntFromInt(big.NewInt(2)),
					},
				},
				EvmServiceResponse: &evmtypes.FilterLogsReply{Logs: []*evmtypes.Log{oversizedLog}},
				ExpectedEvmServiceRequest: normalizeFilterLogsServiceRequest(evmtypes.FilterLogsRequest{
					FilterQuery: evmtypes.FilterQuery{
						FromBlock: big.NewInt(1),
						ToBlock:   big.NewInt(2),
					},
					ConfidenceLevel: primitives.Unconfirmed,
					IsExternal:      true,
				}),
				ExpectedError:   "PayloadSizeLimit",
				ExpectConsensus: true,
				ExpectEvm:       true,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.Name, func(t *testing.T) {
				svc := actions.InitMocks(t)
				if tc.ExpectEvm {
					svc.EvmService.EXPECT().FilterLogs(mock.Anything, tc.ExpectedEvmServiceRequest).Return(tc.EvmServiceResponse, tc.EvmServiceErr).Once()
				}
				chHeight := tc.ChainHeight
				if tc.ExpectConsensus {
					if chHeight == nil {
						chHeight = defaultChainHeight
					}
					svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, request types.Request) (<-chan types.Reply, error) {
						return runAndReturnHandle(ctx, request, chHeight)
					}).Once()
				}
				meta := test.GetMetadataWithFunds()
				if isV2 {
					meta = metaWithHashBasedConsensus(meta)
				}
				ctx := meta.ContextWithCRE(t.Context())
				resp, err := svc.FilterLogs(ctx, meta, tc.Request)
				if tc.ExpectedError != "" {
					require.Error(t, err)
					require.ErrorContains(t, err, tc.ExpectedError)
					return
				}
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(tc.ExpectedResponse, resp.Response, protocmp.Transform()))
				test.ValidateMetering(t, resp.ResponseMetadata, string(metering.FilterLogs))
				if isV2 {
					require.NotNil(t, resp.OCRAttestation)
				} else {
					require.Nil(t, resp.OCRAttestation)
				}
			})
		}
	}
	t.Run("Happy path V1", func(t *testing.T) {
		happyPath(t, false)
	})
	t.Run("Happy path V2", func(t *testing.T) {
		happyPath(t, true)
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
	happyPath := func(t *testing.T, isV2 bool) {
		chainHeight := &types.ChainHeight{
			Latest:    32,
			Safe:      16,
			Finalized: 8,
		}

		tx := &evmtypes.Transaction{
			Nonce:    42,
			Gas:      21000,
			GasPrice: big.NewInt(1),
			Value:    big.NewInt(0),
			Data:     []byte{0x01},
		}
		protoTxA, err := evmcappb.ConvertTransactionToProto(tx)
		require.NoError(t, err)

		testCases := []struct {
			Name                      string
			Request                   *evmcappb.GetTransactionByHashRequest
			EvmServiceResponse        *evmtypes.Transaction
			EvmServiceErr             error
			ExpectedEvmServiceRequest evmtypes.GetTransactionByHashRequest
			ExpectedResponse          *evmcappb.GetTransactionByHashReply
			ExpectedError             string
		}{
			{
				Name:               "lookup returns transaction",
				Request:            &evmcappb.GetTransactionByHashRequest{Hash: bytes.Repeat([]byte{1}, 32)},
				EvmServiceResponse: tx,
				ExpectedEvmServiceRequest: evmtypes.GetTransactionByHashRequest{
					Hash:       evmtypes.Hash(bytes.Repeat([]byte{1}, 32)),
					IsExternal: true,
				},
				ExpectedResponse: &evmcappb.GetTransactionByHashReply{Transaction: protoTxA},
			},
			{
				Name:          "lookup returns rpc error",
				Request:       &evmcappb.GetTransactionByHashRequest{Hash: bytes.Repeat([]byte{1}, 32)},
				EvmServiceErr: errors.New("transaction not found"),
				ExpectedEvmServiceRequest: evmtypes.GetTransactionByHashRequest{
					Hash:       evmtypes.Hash(bytes.Repeat([]byte{1}, 32)),
					IsExternal: true,
				},
				ExpectedError: "transaction not found",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.Name, func(t *testing.T) {
				svc := actions.InitMocks(t)
				svc.EvmService.EXPECT().GetTransactionByHash(mock.Anything, tc.ExpectedEvmServiceRequest).Return(tc.EvmServiceResponse, tc.EvmServiceErr).Once()

				svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, request types.Request) (<-chan types.Reply, error) {
					return runAndReturnHandle(ctx, request, chainHeight)
				}).Once()

				meta := test.GetMetadataWithFunds()
				if isV2 {
					meta = metaWithHashBasedConsensus(meta)
				}
				ctx := meta.ContextWithCRE(t.Context())
				resp, err := svc.GetTransactionByHash(ctx, meta, tc.Request)
				if tc.ExpectedError != "" {
					require.Error(t, err)
					require.ErrorContains(t, err, tc.ExpectedError)
					return
				}
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(tc.ExpectedResponse, resp.Response, protocmp.Transform()))
				test.ValidateMetering(t, resp.ResponseMetadata, string(metering.GetTransactionByHash))
				if isV2 {
					require.NotNil(t, resp.OCRAttestation)
				} else {
					require.Nil(t, resp.OCRAttestation)
				}
			})
		}
	}
	t.Run("Happy path V1", func(t *testing.T) {
		happyPath(t, false)
	})
	t.Run("Happy path V2", func(t *testing.T) {
		happyPath(t, true)
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
	happyPath := func(t *testing.T, isV2 bool) {
		chainHeight := &types.ChainHeight{
			Latest:    32,
			Safe:      16,
			Finalized: 8,
		}

		hash := bytes.Repeat([]byte{1}, 32)

		receipt := &evmtypes.Receipt{
			Status:            12,
			BlockNumber:       big.NewInt(1),
			GasUsed:           21000,
			EffectiveGasPrice: big.NewInt(1),
			Logs:              nil,
		}
		protoRcptA, err := evmcappb.ConvertReceiptToProto(receipt)
		require.NoError(t, err)

		testCases := []struct {
			Name                      string
			Request                   *evmcappb.GetTransactionReceiptRequest
			EvmServiceResponse        *evmtypes.Receipt
			EvmServiceErr             error
			ExpectedEvmServiceRequest evmtypes.GeTransactionReceiptRequest
			ExpectedResponse          *evmcappb.GetTransactionReceiptReply
			ExpectedError             string
		}{
			{
				Name:               "lookup returns receipt",
				Request:            &evmcappb.GetTransactionReceiptRequest{Hash: hash},
				EvmServiceResponse: receipt,
				ExpectedEvmServiceRequest: evmtypes.GeTransactionReceiptRequest{
					Hash:       evmtypes.Hash(hash),
					IsExternal: true,
				},
				ExpectedResponse: &evmcappb.GetTransactionReceiptReply{Receipt: protoRcptA},
			},
			{
				Name:          "lookup returns rpc error",
				Request:       &evmcappb.GetTransactionReceiptRequest{Hash: hash},
				EvmServiceErr: errors.New("receipt not found"),
				ExpectedEvmServiceRequest: evmtypes.GeTransactionReceiptRequest{
					Hash:       evmtypes.Hash(hash),
					IsExternal: true,
				},
				ExpectedError: "receipt not found",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.Name, func(t *testing.T) {
				svc := actions.InitMocks(t)
				svc.EvmService.EXPECT().GetTransactionReceipt(mock.Anything, tc.ExpectedEvmServiceRequest).Return(tc.EvmServiceResponse, tc.EvmServiceErr).Once()

				svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, request types.Request) (<-chan types.Reply, error) {
					return runAndReturnHandle(ctx, request, chainHeight)
				}).Once()

				meta := test.GetMetadataWithFunds()
				if isV2 {
					meta = metaWithHashBasedConsensus(meta)
				}
				ctx := meta.ContextWithCRE(t.Context())
				resp, err := svc.GetTransactionReceipt(ctx, meta, tc.Request)
				if tc.ExpectedError != "" {
					require.Error(t, err)
					require.ErrorContains(t, err, tc.ExpectedError)
					return
				}
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(tc.ExpectedResponse, resp.Response, protocmp.Transform()))
				test.ValidateMetering(t, resp.ResponseMetadata, string(metering.GetTransactionReceipt))
				if isV2 {
					require.NotNil(t, resp.OCRAttestation)
				} else {
					require.Nil(t, resp.OCRAttestation)
				}
			})
		}
	}
	t.Run("Happy path V1", func(t *testing.T) {
		happyPath(t, false)
	})
	t.Run("Happy path V2", func(t *testing.T) {
		happyPath(t, true)
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
	happyPath := func(t *testing.T, isV2 bool) {
		chainHeight := &types.ChainHeight{
			Latest:    32,
			Safe:      16,
			Finalized: 8,
		}

		headerFin := &evmtypes.Header{
			Timestamp: 100,
			Number:    big.NewInt(chainHeight.Finalized),
		}
		protoHeaderFin, err := evmcappb.ConvertHeaderToProto(headerFin)
		require.NoError(t, err)

		headerFixed := &evmtypes.Header{
			Timestamp: 200,
			Number:    big.NewInt(5),
		}
		protoHeaderFixed, err := evmcappb.ConvertHeaderToProto(headerFixed)
		require.NoError(t, err)

		testCases := []struct {
			Name                      string
			Request                   *evmcappb.HeaderByNumberRequest
			EvmServiceResponse        *evmtypes.HeaderByNumberReply
			EvmServiceErr             error
			ExpectedEvmServiceRequest evmtypes.HeaderByNumberRequest
			ExpectedResponse          *evmcappb.HeaderByNumberReply
			ExpectedError             string
		}{
			{
				Name:    "at latest finalized block returns header",
				Request: &evmcappb.HeaderByNumberRequest{BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber)))},
				EvmServiceResponse: &evmtypes.HeaderByNumberReply{
					Header: headerFin,
				},
				ExpectedEvmServiceRequest: evmtypes.HeaderByNumberRequest{
					Number:          big.NewInt(chainHeight.Finalized),
					ConfidenceLevel: primitives.Finalized,
				},
				ExpectedResponse: &evmcappb.HeaderByNumberReply{Header: protoHeaderFin},
			},
			{
				Name:          "at latest finalized block returns error",
				Request:       &evmcappb.HeaderByNumberRequest{BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(int64(rpc.FinalizedBlockNumber)))},
				EvmServiceErr: errors.New("finalized header unavailable"),
				ExpectedEvmServiceRequest: evmtypes.HeaderByNumberRequest{
					Number:          big.NewInt(chainHeight.Finalized),
					ConfidenceLevel: primitives.Finalized,
				},
				ExpectedError: "finalized header unavailable",
			},
			{
				Name:    "at fixed block number returns success",
				Request: &evmcappb.HeaderByNumberRequest{BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(5))},
				EvmServiceResponse: &evmtypes.HeaderByNumberReply{
					Header: headerFixed,
				},
				ExpectedEvmServiceRequest: evmtypes.HeaderByNumberRequest{
					Number:          big.NewInt(5),
					ConfidenceLevel: primitives.Unconfirmed,
				},
				ExpectedResponse: &evmcappb.HeaderByNumberReply{Header: protoHeaderFixed},
			},
			{
				Name:          "at fixed block number returns error",
				Request:       &evmcappb.HeaderByNumberRequest{BlockNumber: valuespb.NewBigIntFromInt(big.NewInt(5))},
				EvmServiceErr: errors.New("missing trie node"),
				ExpectedEvmServiceRequest: evmtypes.HeaderByNumberRequest{
					Number:          big.NewInt(5),
					ConfidenceLevel: primitives.Unconfirmed,
				},
				ExpectedError: "missing trie node",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.Name, func(t *testing.T) {
				svc := actions.InitMocks(t)
				svc.EvmService.EXPECT().HeaderByNumber(mock.Anything, tc.ExpectedEvmServiceRequest).Return(tc.EvmServiceResponse, tc.EvmServiceErr).Once()

				svc.ConsensusHandler.EXPECT().Handle(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, request types.Request) (<-chan types.Reply, error) {
					return runAndReturnHandle(ctx, request, chainHeight)
				}).Once()

				meta := test.GetMetadataWithFunds()
				if isV2 {
					meta = metaWithHashBasedConsensus(meta)
				}
				ctx := meta.ContextWithCRE(t.Context())
				resp, err := svc.HeaderByNumber(ctx, meta, tc.Request)
				if tc.ExpectedError != "" {
					require.Error(t, err)
					require.ErrorContains(t, err, tc.ExpectedError)
					return
				}
				require.NoError(t, err)
				require.Empty(t, cmp.Diff(tc.ExpectedResponse, resp.Response, protocmp.Transform()))
				test.ValidateMetering(t, resp.ResponseMetadata, string(metering.HeaderByNumber))
				if isV2 {
					require.NotNil(t, resp.OCRAttestation)
				} else {
					require.Nil(t, resp.OCRAttestation)
				}
			})
		}
	}
	t.Run("Happy path V1", func(t *testing.T) {
		happyPath(t, false)
	})
	t.Run("Happy path V2", func(t *testing.T) {
		happyPath(t, true)
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

func normalizeFilterLogsServiceRequest(req evmtypes.FilterLogsRequest) evmtypes.FilterLogsRequest {
	if len(req.FilterQuery.Topics) == 0 {
		req.FilterQuery.Topics = make([][]evmtypes.Hash, 0)
	}
	if len(req.FilterQuery.Addresses) == 0 {
		req.FilterQuery.Addresses = make([]evmtypes.Address, 0)
	}
	return req
}

// Aligns with cresettings default FeatureMultiTriggerExecutionIDsActivePeriod ([2100-01-01, 2101-01-01]) so hash-based OCR path is active.
func metaWithHashBasedConsensus(base capabilities.RequestMetadata) capabilities.RequestMetadata {
	base.ExecutionTimestamp = time.Date(2100, 6, 1, 0, 0, 0, 0, time.UTC)
	return base
}
