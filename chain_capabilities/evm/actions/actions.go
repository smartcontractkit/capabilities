package actions

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	chaincommonpb "github.com/smartcontractkit/chainlink-common/pkg/loop/chain-common"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	ctypes "github.com/smartcontractkit/chain_capabilities/evm/consensus/types"
)

type ConsensusReader interface {
	Read(ctx context.Context, request ctypes.Request) (<-chan []byte, error)
}

type EVM struct {
	types.EVMService
	consensusReader ConsensusReader
}

func NewEVM(evmService types.EVMService) EVM {
	return EVM{EVMService: evmService}
}

func requestID(meta capabilities.RequestMetadata) string {
	return meta.WorkflowExecutionID + ":" + meta.ReferenceID
}

func (e EVM) CallContract(ctx context.Context, meta capabilities.RequestMetadata, input *evmservice.CallContractRequest) (*evmservice.CallContractReply, error) {
	callMsg, err := evmservice.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, err
	}

	blockNumber, requiresLocking, err := normalizeBlockNumber(input.GetBlockNumber())
	if err != nil {
		return nil, err
	}

	var request ctypes.Request
	if requiresLocking {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *evmservice.ChainHeight) ([]byte, error) {
			// TODO: PLEX-1571 guarantee finality/safety of observed data for load balanced RPCs
			callBlockNumber, err := getCallBlockNumber(blockNumber, height)
			if err != nil {
				return nil, fmt.Errorf("error getting call block number: %w", err)
			}

			// TODO: PLEX-1558 agree on RPC error content
			return e.EVMService.CallContract(ctx, callMsg, callBlockNumber)
		})
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return e.EVMService.CallContract(ctx, callMsg, big.NewInt(int64(blockNumber)))
		})
	}

	resultCh, err := e.consensusReader.Read(ctx, request)
	if err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case data := <-resultCh:
		return &evmservice.CallContractReply{Data: data}, nil
	}
}

func (e EVM) filterLogsToRequest(ctx context.Context, meta capabilities.RequestMetadata, req *evmservice.FilterLogsRequest) (ctypes.Request, error) {
	ethFilterQuery, err := evmservice.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, err
	}

	filterLogs := func(ctx context.Context, query evmtypes.FilterQuery) ([]byte, error) {
		ethLogs, err := e.EVMService.FilterLogs(ctx, query)
		if err != nil {
			return nil, err
		}

		return proto.Marshal(&evmservice.FilterLogsReply{Logs: evmservice.ConvertLogsToProto(ethLogs)})
	}

	// TODO: PLEX-1559 add validation for block range size and size of returned payload
	if len(req.FilterQuery.BlockHash) != 0 {
		if ethFilterQuery.FromBlock != nil || ethFilterQuery.ToBlock != nil {
			return nil, errors.New("cannot specify both block hash and block range")
		}

		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery)
		}), nil
	}

	fromBlock, fromBlockRequiresLocking, err := normalizeBlockNumber(req.FilterQuery.FromBlock)
	if err != nil {
		return nil, fmt.Errorf("fromBlock is invalid: %w", err)
	}

	toBlock, toBlockRequiresLocking, err := normalizeBlockNumber(req.FilterQuery.ToBlock)
	if err != nil {
		return nil, fmt.Errorf("toBlock is invalid: %w", err)
	}

	if !fromBlockRequiresLocking && !toBlockRequiresLocking {
		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery)
		}), nil
	}

	return ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *evmservice.ChainHeight) ([]byte, error) {
		callFromBlock, err := getCallBlockNumber(fromBlock, height)
		if err != nil {
			return nil, fmt.Errorf("error getting callFromBlock: %w", err)
		}

		callToBlock, err := getCallBlockNumber(toBlock, height)
		if err != nil {
			return nil, fmt.Errorf("error getting callToBlock: %w", err)
		}

		ethFilterQuery.FromBlock = big.NewInt(callFromBlock.Int64())
		ethFilterQuery.ToBlock = big.NewInt(callToBlock.Int64())

		return filterLogs(ctx, ethFilterQuery)
	}), nil
}

func (e EVM) FilterLogs(ctx context.Context, meta capabilities.RequestMetadata, req *evmservice.FilterLogsRequest) (*evmservice.FilterLogsReply, error) {
	request, err := e.filterLogsToRequest(ctx, meta, req)
	if err != nil {
		return nil, err
	}

	var reply evmservice.FilterLogsReply
	err = e.getReply(ctx, request, &reply)
	if err != nil {
		return nil, err
	}

	return &reply, nil
}

func (e EVM) BalanceAt(ctx context.Context, meta capabilities.RequestMetadata, req *evmservice.BalanceAtRequest) (*evmservice.BalanceAtReply, error) {
	blockNumber, requiresLocking, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, err
	}

	balanceAt := func(ctx context.Context, height *evmservice.ChainHeight) ([]byte, error) {
		callBlockNumber, err := getCallBlockNumber(blockNumber, height)
		if err != nil {
			return nil, fmt.Errorf("error getting call block number: %w", err)
		}
		balance, err := e.EVMService.BalanceAt(ctx, evmtypes.Address(req.GetAccount()), callBlockNumber)
		if err != nil {
			return nil, err
		}

		pbBalance := valuespb.NewBigIntFromInt(balance)
		return proto.Marshal(pbBalance)
	}

	var request ctypes.Request
	if requiresLocking {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), balanceAt)
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return balanceAt(ctx, nil)
		})
	}

	var balance valuespb.BigInt
	if err := e.getReply(ctx, request, &balance); err != nil {
		return nil, err
	}

	return &evmservice.BalanceAtReply{Balance: &balance}, nil
}

func (e EVM) EstimateGas(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.EstimateGasRequest) (*evmservice.EstimateGasReply, error) {
	// TODO: PLEX-1470 implement aggregatable method handling
	callMsg, err := evmservice.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, err
	}

	estimate, err := e.EVMService.EstimateGas(etx, callMsg)
	if err != nil {
		return &evmservice.EstimateGasReply{}, err
	}

	return &evmservice.EstimateGasReply{Gas: estimate}, nil
}

func (e EVM) getReply(ctx context.Context, request ctypes.Request, into proto.Message) (err error) {
	resultCh, err := e.consensusReader.Read(ctx, request)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case data := <-resultCh:
		return proto.Unmarshal(data, into)
	}
}

func (e EVM) GetTransactionByHash(ctx context.Context, meta capabilities.RequestMetadata, req *evmservice.GetTransactionByHashRequest) (*evmservice.GetTransactionByHashReply, error) {
	hash, err := evmservice.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		tx, err := e.EVMService.GetTransactionByHash(ctx, hash)
		if err != nil {
			return nil, err
		}

		protoTx, err := evmservice.ConvertTransactionToProto(tx)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoTx)
	})

	var tx evmservice.Transaction
	if err := e.getReply(ctx, request, &tx); err != nil {
		return nil, err
	}
	return &evmservice.GetTransactionByHashReply{Transaction: &tx}, nil
}

func (e EVM) GetTransactionReceipt(ctx context.Context, meta capabilities.RequestMetadata, req *evmservice.GetTransactionReceiptRequest) (*evmservice.GetTransactionReceiptReply, error) {
	hash, err := evmservice.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		receipt, err := e.EVMService.GetTransactionReceipt(ctx, hash)
		if err != nil {
			return nil, err
		}

		protoReceipt, err := evmservice.ConvertReceiptToProto(receipt)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoReceipt)
	})

	var receipt evmservice.Receipt
	if err := e.getReply(ctx, request, &receipt); err != nil {
		return nil, err
	}
	return &evmservice.GetTransactionReceiptReply{Receipt: &receipt}, nil
}

func (e EVM) LatestAndFinalizedHead(etx context.Context, _ capabilities.RequestMetadata, _ *emptypb.Empty) (*evmservice.LatestAndFinalizedHeadReply, error) {
	// TODO implement as part of PLEX-1560
	latest, finalized, err := e.EVMService.LatestAndFinalizedHead(etx)
	if err != nil {
		return nil, err
	}

	return &evmservice.LatestAndFinalizedHeadReply{
		Latest:    evmservice.ConvertHeadToProto(latest),
		Finalized: evmservice.ConvertHeadToProto(finalized),
	}, nil
}

// TODO finalise the signature PLEX-1482
func (e EVM) QueryTrackedLogs(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.QueryTrackedLogsRequest) (*evmservice.QueryTrackedLogsReply, error) {
	expression, err := evmservice.ConvertExpressionsFromProto(req.Expression)
	if err != nil {
		return nil, err
	}

	limitAndSort, err := chaincommonpb.ConvertLimitAndSortFromProto(req.LimitAndSort)
	if err != nil {
		return nil, err
	}

	confidenceLevel, err := chaincommonpb.ConfidenceFromProto(req.ConfidenceLevel)
	if err != nil {
		return nil, err
	}

	// TODO what does confidence level do here when we have block ranges, should the impl. throw an error if a block range is outside of the specifice confidence level?
	// TODO is an OCR round needed to validate block hashes on the log response, probably is too much, probably just require the block range to always be specified and rely on exact match
	result, err := e.EVMService.QueryTrackedLogs(etx, expression, limitAndSort, confidenceLevel)
	if err != nil {
		return nil, err
	}

	return &evmservice.QueryTrackedLogsReply{Logs: evmservice.ConvertLogsToProto(result)}, nil
}

func (e EVM) RegisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.RegisterLogTrackingRequest) (*emptypb.Empty, error) {
	filter, err := evmservice.ConvertLPFilterFromProto(req.GetFilter())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, e.EVMService.RegisterLogTracking(etx, filter)
}

func (e EVM) UnregisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.UnregisterLogTrackingRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, e.EVMService.UnregisterLogTracking(etx, req.FilterName)
}

func normalizeBlockNumber(pbBlockNumber *valuespb.BigInt) (number rpcBlockNumber, requiresLocking bool, err error) {
	if pbBlockNumber == nil {
		return latestBlockNumber, true, nil
	}

	bigBlockNumber := valuespb.NewIntFromBigInt(pbBlockNumber)
	if !bigBlockNumber.IsInt64() {
		return 0, false, fmt.Errorf("block number %s is not an int64", bigBlockNumber)
	}

	blockNumber := rpcBlockNumber(bigBlockNumber.Int64())
	if blockNumber > 0 {
		return blockNumber, false, nil
	}

	switch blockNumber {
	case safeBlockNumber, finalizedBlockNumber, latestBlockNumber:
		return blockNumber, true, nil
	default:
		return 0, false, fmt.Errorf("block number %d is not supported", blockNumber)
	}
}

func getCallBlockNumber(requestedBlockNumber rpcBlockNumber, chainHeight *evmservice.ChainHeight) (*big.Int, error) {
	switch requestedBlockNumber {
	case latestBlockNumber, safeBlockNumber, finalizedBlockNumber:
	default:
		return big.NewInt(int64(requestedBlockNumber)), nil
	}

	if chainHeight == nil {
		return nil, fmt.Errorf("chain height is nil")
	}

	switch requestedBlockNumber {
	case latestBlockNumber:
		return big.NewInt(chainHeight.Latest), nil
	case safeBlockNumber:
		return big.NewInt(chainHeight.Safe), nil
	case finalizedBlockNumber:
		return big.NewInt(chainHeight.Finalized), nil
	default:
		return nil, fmt.Errorf("unexpected block number %d", requestedBlockNumber)
	}
}
