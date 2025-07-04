package actions

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/shopspring/decimal"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/pb"

	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
)

type ConsensusHandler interface {
	// Handle - returns a channel to the result of `request.GetObservation()`. This result is consistent across all nodes in
	// the DON, even if individual RPC states differ.
	Handle(ctx context.Context, request ctypes.Request) (<-chan any, error)
}

type EVM struct {
	types.EVMService
	ConsensusHandler         ConsensusHandler
	keystoneForwarderAddress common.Address
	forwarderClient          contracts.CREForwarderClient
	ReceiverGasMinimum       uint64

	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder
}

func NewEVM(cfg config.Config, evmService types.EVMService, lggr logger.Logger, beholderProcessor beholder.ProtoProcessor,
	messageBuilder *monitoring.MessageBuilder, handler ConsensusHandler) (EVM, error) {
	keystoneForwarderAddress := common.HexToAddress(cfg.CREForwarderAddress)
	kfc, err := contracts.NewCREForwarderClient(evmService, keystoneForwarderAddress, lggr)
	if err != nil {
		return EVM{}, err
	}

	return EVM{
		EVMService:               evmService,
		keystoneForwarderAddress: keystoneForwarderAddress,
		forwarderClient:          kfc,
		ReceiverGasMinimum:       cfg.ReceiverGasMinimum,
		lggr:                     lggr,
		beholderProcessor:        beholderProcessor,
		messageBuilder:           messageBuilder,
		ConsensusHandler:         handler,
	}, nil
}

func requestID(meta capabilities.RequestMetadata) string {
	return meta.WorkflowExecutionID + ":" + meta.ReferenceID
}

func (e EVM) CallContract(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	input *pb.CallContractRequest,
) (*pb.CallContractReply, error) {
	read := monitoring.ReadRequest{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	callMsg, err := pb.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, err
	}

	blockNumber, needsBlockHeightConsensus, err := normalizeBlockNumber(input.GetBlockNumber())
	if err != nil {
		return nil, err
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractInitiated(read, callMsg, blockNumber.Int64()))

	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
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

	data, err := readType[[]byte](ctx, e.ConsensusHandler, request)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractError(read, callMsg, blockNumber.Int64(), "Failed to read CallContract", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read CallContract", e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractSuccess(read, callMsg, blockNumber.Int64()))
	return &pb.CallContractReply{Data: data}, nil
}

func (e EVM) filterLogsToRequest(meta capabilities.RequestMetadata, ethFilterQuery evmtypes.FilterQuery) (ctypes.Request, error) {
	filterLogs := func(ctx context.Context, query evmtypes.FilterQuery) ([]byte, error) {
		ethLogs, err := e.EVMService.FilterLogs(ctx, query)
		if err != nil {
			return nil, err
		}

		return proto.Marshal(&pb.FilterLogsReply{Logs: pb.ConvertLogsToProto(ethLogs)})
	}

	// TODO: PLEX-1559 add validation for block range size and size of returned payload
	if ethFilterQuery.BlockHash != (evmtypes.Hash{}) {
		if ethFilterQuery.FromBlock != nil || ethFilterQuery.ToBlock != nil {
			return nil, errors.New("cannot specify both block hash and block range")
		}

		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery)
		}), nil
	}

	fromBlock, fromNeedsBlockHeightConsensus, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.FromBlock))
	if err != nil {
		return nil, fmt.Errorf("fromBlock is invalid: %w", err)
	}

	toBlock, toNeedsBlockHeightConsensus, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.ToBlock))
	if err != nil {
		return nil, fmt.Errorf("toBlock is invalid: %w", err)
	}

	if !fromNeedsBlockHeightConsensus && !toNeedsBlockHeightConsensus {
		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery)
		}), nil
	}

	return ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
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

func (e EVM) FilterLogs(ctx context.Context, meta capabilities.RequestMetadata, req *pb.FilterLogsRequest) (*pb.FilterLogsReply, error) {
	read := monitoring.ReadRequest{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	ethFilterQuery, err := pb.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, err
	}

	request, err := e.filterLogsToRequest(meta, ethFilterQuery)
	if err != nil {
		return nil, err
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsInitiated(read, ethFilterQuery))

	var reply pb.FilterLogsReply
	err = e.readProto(ctx, request, &reply)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsError(read, ethFilterQuery, "Failed to FilterLogs", err.Error()))
		return nil, err
	}

	// G115: integer overflow conversion int -> int32 (gosec)
	// nolint:gosec
	monitoring.LogAndEmitSuccess(ctx, "Successfully executed FilterLogs", e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsSuccess(read, ethFilterQuery, int32(len(reply.Logs))))
	return &reply, nil
}

func (e EVM) BalanceAt(ctx context.Context, meta capabilities.RequestMetadata, req *pb.BalanceAtRequest) (*pb.BalanceAtReply, error) {
	read := monitoring.ReadRequest{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, err
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildBalanceAtInitiated(read, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64()))

	balanceAt := func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
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
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), balanceAt)
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return balanceAt(ctx, nil)
		})
	}

	balance := new(valuespb.BigInt)
	if err := e.readProto(ctx, request, balance); err != nil {
		errMsg := e.messageBuilder.BuildBalanceAtError(read, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), "Failed to read BalanceAt", err.Error())
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, errMsg)
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read BalanceAt", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildBalanceAtSuccess(read, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), valuespb.NewIntFromBigInt(balance)))
	return &pb.BalanceAtReply{Balance: balance}, nil
}

func (e EVM) EstimateGas(ctx context.Context, meta capabilities.RequestMetadata, req *pb.EstimateGasRequest) (*pb.EstimateGasReply, error) {
	read := monitoring.ReadRequest{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	msg, err := pb.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, err
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildEstimateGasInitiated(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data))

	request := ctypes.NewAggregatableRequest(requestID(meta), func(ctx context.Context) (*ctypes.AggregatableObservation, error) {
		rawEstimate, err := e.EVMService.EstimateGas(ctx, msg)
		if err != nil {
			return nil, err
		}

		estimate := &valuespb.Decimal{
			Coefficient: valuespb.NewBigIntFromInt(big.NewInt(0).SetUint64(rawEstimate)),
			Exponent:    0,
		}

		return &ctypes.AggregatableObservation{
			Method: ctypes.AggregationMethodFPlusOneHighest,
			Value:  estimate,
		}, nil
	})

	rawEstimate, err := readDecimal(ctx, e.ConsensusHandler, request)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildEstimateGasError(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, "Failed to execute EstimateGas", err.Error()))
		return nil, err
	}

	logMsg := e.messageBuilder.BuildEstimateGasSuccess(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, rawEstimate.BigInt().Int64())
	monitoring.LogAndEmitSuccess(ctx, "Successfully read EstimateGas", e.lggr, e.beholderProcessor, logMsg)
	return &pb.EstimateGasReply{Gas: rawEstimate.BigInt().Uint64()}, nil
}

func (e EVM) GetTransactionByHash(ctx context.Context, meta capabilities.RequestMetadata, req *pb.GetTransactionByHashRequest) (*pb.GetTransactionByHashReply, error) {
	read := monitoring.ReadRequest{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := pb.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionByHashInitiated(read, common.Bytes2Hex(hash[:])))
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		tx, err := e.EVMService.GetTransactionByHash(ctx, hash)
		if err != nil {
			return nil, err
		}

		protoTx, err := pb.ConvertTransactionToProto(tx)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoTx)
	})

	var tx pb.Transaction
	if err := e.readProto(ctx, request, &tx); err != nil {
		errMsg := e.messageBuilder.BuildGetTransactionByHashError(read, common.Bytes2Hex(hash[:]), "Failed to execute GetTransactionByHash", err.Error())
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, errMsg)
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionByHash", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionByHashSuccess(read, common.Bytes2Hex(hash[:]), &tx))
	return &pb.GetTransactionByHashReply{Transaction: &tx}, nil
}

func (e EVM) GetTransactionReceipt(ctx context.Context, meta capabilities.RequestMetadata, req *pb.GetTransactionReceiptRequest) (*pb.GetTransactionReceiptReply, error) {
	read := monitoring.ReadRequest{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := pb.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptInitiated(read, common.Bytes2Hex(hash[:])))
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		receipt, err := e.EVMService.GetTransactionReceipt(ctx, hash)
		if err != nil {
			return nil, err
		}

		protoReceipt, err := pb.ConvertReceiptToProto(receipt)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoReceipt)
	})

	var receipt pb.Receipt
	if err := e.readProto(ctx, request, &receipt); err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptError(read, common.Bytes2Hex(hash[:]), "Failed to get latest and finalized head", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionReceiptSuccess", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionReceiptSuccess(read, common.Bytes2Hex(hash[:]), &receipt))
	return &pb.GetTransactionReceiptReply{Receipt: &receipt}, nil
}

// TODO implement as part of PLEX-1560
func (e EVM) LatestAndFinalizedHead(
	ctx context.Context,
	req capabilities.RequestMetadata,
	_ *emptypb.Empty,
) (*pb.LatestAndFinalizedHeadReply, error) {
	read := monitoring.ReadRequest{TsStart: time.Now().UnixMilli(), RequestMetadata: req}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildLatestAndFinalizedHeadInitiated(read))
	latest, fin, err := e.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildLatestAndFinalizedHeadError(read, "Failed to get latest and finalized head", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read LatestAndFinalizedHead", e.lggr, e.beholderProcessor, e.messageBuilder.BuildLatestAndFinalizedHeadSuccess(read, latest, fin))
	return &pb.LatestAndFinalizedHeadReply{Latest: pb.ConvertHeadToProto(latest), Finalized: pb.ConvertHeadToProto(fin)}, nil
}

func (e EVM) RegisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *pb.RegisterLogTrackingRequest) (*emptypb.Empty, error) {
	filter, err := pb.ConvertLPFilterFromProto(req.GetFilter())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, e.EVMService.RegisterLogTracking(etx, filter)
}

func (e EVM) UnregisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *pb.UnregisterLogTrackingRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, e.EVMService.UnregisterLogTracking(etx, req.FilterName)
}

// normalizeBlockNumber - returns:
// number - normalized block number converted to a corresponding tag, if possible
// needsBlockHeightConsensus - true, if DON Nodes need to agree on common height for corresponding tag, before agreeing on request reply.
func normalizeBlockNumber(pbBlockNumber *valuespb.BigInt) (number rpc.BlockNumber, needsBlockHeightConsensus bool, err error) {
	// Replicate EthClient API, that treats nil block number as latest
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

func getCallBlockNumber(requestedBlockNumber rpc.BlockNumber, chainHeight *ctypes.ChainHeight) (*big.Int, error) {
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

func readDecimal(ctx context.Context, handler ConsensusHandler, request ctypes.Request) (decimal.Decimal, error) {
	rawDecimal, err := readType[*valuespb.Decimal](ctx, handler, request)
	if err != nil {
		return decimal.Decimal{}, err
	}

	return decimal.NewFromBigInt(valuespb.NewIntFromBigInt(rawDecimal.Coefficient), rawDecimal.Exponent), nil
}

func (e EVM) readProto(ctx context.Context, request ctypes.Request, into proto.Message) (err error) {
	data, err := readType[[]byte](ctx, e.ConsensusHandler, request)
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, into)
}

func readType[T any](ctx context.Context, reader ConsensusHandler, request ctypes.Request) (T, error) {
	var zero T
	resultCh, err := reader.Handle(ctx, request)
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
