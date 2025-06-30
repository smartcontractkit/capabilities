package actions

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/shopspring/decimal"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts"
)

type ConsensusReader interface {
	Read(ctx context.Context, request ctypes.Request) (<-chan any, error)
}

type EVM struct {
	types.EVMService
	ConsensusReader          ConsensusReader
	keystoneForwarderAddress common.Address
	forwarderClient          contracts.CREForwarderClient
	lggr                     logger.Logger
	ReceiverGasMinimum       uint64
}

func NewEVM(cfg config.Config, evmService types.EVMService, logger logger.Logger) (EVM, error) {
	keystoneForwarderAddress := common.HexToAddress(cfg.CREForwarderAddress)
	kfc, err := contracts.NewCREForwarderClient(evmService, keystoneForwarderAddress, logger)
	if err != nil {
		return EVM{}, err
	}
	return EVM{EVMService: evmService, keystoneForwarderAddress: keystoneForwarderAddress, ReceiverGasMinimum: cfg.ReceiverGasMinimum, lggr: logger, forwarderClient: kfc}, nil
}

func requestID(meta capabilities.RequestMetadata) string {
	return meta.WorkflowExecutionID + ":" + meta.ReferenceID
}

// TODO finalise the signature PLEX-1482
func (e EVM) CallContract(ctx context.Context, meta capabilities.RequestMetadata, input *evmcappb.CallContractRequest) (*evmcappb.CallContractReply, error) {
	callMsg, err := evmcappb.ConvertCallMsgFromProto(input.GetCall())
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

	data, err := readType[[]byte](ctx, e.ConsensusReader, request)
	if err != nil {
		return nil, err
	}

	return &evmcappb.CallContractReply{Data: data}, nil
}

func (e EVM) filterLogsToRequest(ctx context.Context, meta capabilities.RequestMetadata, req *evmcappb.FilterLogsRequest) (ctypes.Request, error) {
	ethFilterQuery, err := evmcappb.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, err
	}

	filterLogs := func(ctx context.Context, query evmtypes.FilterQuery) ([]byte, error) {
		ethLogs, err := e.EVMService.FilterLogs(ctx, query)
		if err != nil {
			return nil, err
		}

		return proto.Marshal(&evmcappb.FilterLogsReply{Logs: evmcappb.ConvertLogsToProto(ethLogs)})
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

func (e EVM) FilterLogs(ctx context.Context, meta capabilities.RequestMetadata, req *evmcappb.FilterLogsRequest) (*evmcappb.FilterLogsReply, error) {
	request, err := e.filterLogsToRequest(ctx, meta, req)
	if err != nil {
		return nil, err
	}
	var reply evmcappb.FilterLogsReply
	err = e.readProto(ctx, request, &reply)
	if err != nil {
		return nil, err
	}

	return &reply, nil
}

func (e EVM) BalanceAt(ctx context.Context, meta capabilities.RequestMetadata, req *evmcappb.BalanceAtRequest) (*evmcappb.BalanceAtReply, error) {
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
	if err := e.readProto(ctx, request, &balance); err != nil {
		return nil, err
	}

	return &evmcappb.BalanceAtReply{Balance: &balance}, nil
}

func (e EVM) EstimateGas(ctx context.Context, meta capabilities.RequestMetadata, req *evmcappb.EstimateGasRequest) (*evmcappb.EstimateGasReply, error) {
	callMsg, err := evmcappb.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, err
	}

	request := ctypes.NewAggregatabelRequest(requestID(meta), func(ctx context.Context) (*evmservice.AggregatableObservation, error) {
		rawEstimate, err := e.EVMService.EstimateGas(ctx, callMsg)
		if err != nil {
			return nil, err
		}

		estimate := &valuespb.Decimal{
			Coefficient: valuespb.NewBigIntFromInt(big.NewInt(0).SetUint64(rawEstimate)),
			Exponent:    0,
		}

		return &evmservice.AggregatableObservation{
			Method: ctypes.AggregationMethodFPlusOneHighest,
			Value:  estimate,
		}, nil
	})

	rawEstimate, err := readDecimal(ctx, e.ConsensusReader, request)
	if err != nil {
		return nil, err
	}

	return &evmcappb.EstimateGasReply{Gas: rawEstimate.BigInt().Uint64()}, nil
}

func readDecimal(ctx context.Context, reader ConsensusReader, request ctypes.Request) (decimal.Decimal, error) {
	rawDecimal, err := readType[*valuespb.Decimal](ctx, reader, request)
	if err != nil {
		return decimal.Decimal{}, err
	}

	return decimal.NewFromBigInt(valuespb.NewIntFromBigInt(rawDecimal.Coefficient), rawDecimal.Exponent), nil
}

func (e EVM) readProto(ctx context.Context, request ctypes.Request, into proto.Message) (err error) {
	data, err := readType[[]byte](ctx, e.ConsensusReader, request)
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, into)
}

func readType[T any](ctx context.Context, reader ConsensusReader, request ctypes.Request) (T, error) {
	var zero T
	resultCh, err := reader.Read(ctx, request)
	if err != nil {
		return zero, err
	}

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case rawData := <-resultCh:
		data, ok := rawData.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected result type: expected %T, got %T", zero, rawData)
		}

		return data, nil
	}
}

func (e EVM) GetTransactionByHash(ctx context.Context, meta capabilities.RequestMetadata, req *evmcappb.GetTransactionByHashRequest) (*evmcappb.GetTransactionByHashReply, error) {
	hash, err := evmcappb.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		tx, err := e.EVMService.GetTransactionByHash(ctx, hash)
		if err != nil {
			return nil, err
		}

		protoTx, err := evmcappb.ConvertTransactionToProto(tx)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoTx)
	})

	var tx evmcappb.Transaction
	if err := e.readProto(ctx, request, &tx); err != nil {
		return nil, err
	}
	return &evmcappb.GetTransactionByHashReply{Transaction: &tx}, nil
}

func (e EVM) GetTransactionReceipt(ctx context.Context, meta capabilities.RequestMetadata, req *evmcappb.GetTransactionReceiptRequest) (*evmcappb.GetTransactionReceiptReply, error) {
	hash, err := evmcappb.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		receipt, err := e.EVMService.GetTransactionReceipt(ctx, hash)
		if err != nil {
			return nil, err
		}

		protoReceipt, err := evmcappb.ConvertReceiptToProto(receipt)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoReceipt)
	})

	var receipt evmcappb.Receipt
	if err := e.readProto(ctx, request, &receipt); err != nil {
		return nil, err
	}
	return &evmcappb.GetTransactionReceiptReply{Receipt: &receipt}, nil
}

func (e EVM) LatestAndFinalizedHead(etx context.Context, _ capabilities.RequestMetadata, _ *emptypb.Empty) (*evmcappb.LatestAndFinalizedHeadReply, error) {
	// TODO implement as part of PLEX-1560
	latest, finalized, err := e.EVMService.LatestAndFinalizedHead(etx)
	if err != nil {
		return nil, err
	}

	return &evmcappb.LatestAndFinalizedHeadReply{
		Latest:    evmcappb.ConvertHeadToProto(latest),
		Finalized: evmcappb.ConvertHeadToProto(finalized),
	}, nil
}

func (e EVM) RegisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *evmcappb.RegisterLogTrackingRequest) (*emptypb.Empty, error) {
	filter, err := evmcappb.ConvertLPFilterFromProto(req.GetFilter())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, e.EVMService.RegisterLogTracking(etx, filter)
}

func (e EVM) UnregisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *evmcappb.UnregisterLogTrackingRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, e.EVMService.UnregisterLogTracking(etx, req.FilterName)
}

func normalizeBlockNumber(pbBlockNumber *valuespb.BigInt) (number rpc.BlockNumber, requiresLocking bool, err error) {
	if pbBlockNumber == nil {
		return rpc.LatestBlockNumber, true, nil
	}

	bigBlockNumber := valuespb.NewIntFromBigInt(pbBlockNumber)
	if !bigBlockNumber.IsInt64() {
		return 0, false, fmt.Errorf("block number %s is not an int64", bigBlockNumber)
	}

	blockNumber := rpc.BlockNumber(bigBlockNumber.Int64())
	if blockNumber > 0 {
		return blockNumber, false, nil
	}

	switch blockNumber {
	case rpc.SafeBlockNumber, rpc.FinalizedBlockNumber, rpc.LatestBlockNumber:
		return blockNumber, true, nil
	default:
		return 0, false, fmt.Errorf("block number %d is not supported", blockNumber)
	}
}

func getCallBlockNumber(requestedBlockNumber rpc.BlockNumber, chainHeight *evmservice.ChainHeight) (*big.Int, error) {
	switch requestedBlockNumber {
	case rpc.LatestBlockNumber, rpc.SafeBlockNumber, rpc.FinalizedBlockNumber:
	default:
		return big.NewInt(int64(requestedBlockNumber)), nil
	}

	if chainHeight == nil {
		return nil, fmt.Errorf("chain height is nil")
	}

	switch requestedBlockNumber {
	case rpc.LatestBlockNumber:
		return big.NewInt(chainHeight.Latest), nil
	case rpc.SafeBlockNumber:
		return big.NewInt(chainHeight.Safe), nil
	case rpc.FinalizedBlockNumber:
		return big.NewInt(chainHeight.Finalized), nil
	default:
		return nil, fmt.Errorf("unexpected block number %d", requestedBlockNumber)
	}
}
