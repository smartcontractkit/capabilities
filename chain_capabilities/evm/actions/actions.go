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

	"github.com/smartcontractkit/chainlink-common/pkg/types/query/primitives"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	ctypes "github.com/smartcontractkit/capabilities/chain_capabilities/evm/consensus/types"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/internal/contracts"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type ConsensusHandler interface {
	// Handle - returns a channel to the result of `request.GetObservation()`. This result is consistent across all nodes in
	// the DON, even if individual RPC states differ.
	Handle(ctx context.Context, request ctypes.Request) (<-chan ctypes.Reply, error)
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
	input *evm.CallContractRequest,
) (*capabilities.ResponseAndMetadata[*evm.CallContractReply], error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}

	callMsg, err := evm.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, err
	}

	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(input.GetBlockNumber())
	if err != nil {
		return nil, err
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractInitiated(telemetryContext, callMsg, blockNumber.Int64()))

	callContract := func(ctx context.Context, blockNumber *big.Int) ([]byte, error) {
		// TODO: PLEX-1558 agree on RPC error content
		resp, err := e.EVMService.CallContract(ctx, evmtypes.CallContractRequest{
			Msg:             callMsg,
			BlockNumber:     blockNumber,
			ConfidenceLevel: confidenceLevel,
			IsExternal:      true,
		})
		if err != nil {
			return nil, err
		}
		return resp.Data, nil
	}

	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
			callBlockNumber, err := getCallBlockNumber(blockNumber, height)
			if err != nil {
				return nil, fmt.Errorf("error getting call block number: %w", err)
			}

			return callContract(ctx, callBlockNumber)
		})
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return callContract(ctx, big.NewInt(blockNumber.Int64()))
		})
	}

	data, err := readType[[]byte](ctx, e.ConsensusHandler, request)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractError(telemetryContext, callMsg, blockNumber.Int64(), "Failed to read CallContract", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read CallContract", e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractSuccess(telemetryContext, callMsg, blockNumber.Int64()))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.CallContractReply]{
		Response:         &evm.CallContractReply{Data: data},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e EVM) filterLogsToRequest(meta capabilities.RequestMetadata, ethFilterQuery evmtypes.FilterQuery) (ctypes.Request, error) {
	filterLogs := func(ctx context.Context, query evmtypes.FilterQuery, confidenceLevel primitives.ConfidenceLevel) ([]byte, error) {
		reply, err := e.EVMService.FilterLogs(ctx, evmtypes.FilterLogsRequest{
			FilterQuery:     query,
			ConfidenceLevel: confidenceLevel,
			IsExternal:      true,
		})
		if err != nil {
			return nil, err
		}

		return proto.Marshal(&evm.FilterLogsReply{Logs: evm.ConvertLogsToProto(reply.Logs)})
	}

	// TODO: PLEX-1559 add validation for block range size and size of returned payload
	if ethFilterQuery.BlockHash != (evmtypes.Hash{}) {
		if ethFilterQuery.FromBlock != nil || ethFilterQuery.ToBlock != nil {
			return nil, errors.New("cannot specify both block hash and block range")
		}

		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery, primitives.Unconfirmed)
		}), nil
	}

	fromBlock, fromNeedsBlockHeightConsensus, _, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.FromBlock))
	if err != nil {
		return nil, fmt.Errorf("fromBlock is invalid: %w", err)
	}

	toBlock, toNeedsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(valuespb.NewBigIntFromInt(ethFilterQuery.ToBlock))
	if err != nil {
		return nil, fmt.Errorf("toBlock is invalid: %w", err)
	}

	if !fromNeedsBlockHeightConsensus && !toNeedsBlockHeightConsensus {
		return ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return filterLogs(ctx, ethFilterQuery, confidenceLevel)
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

		return filterLogs(ctx, ethFilterQuery, confidenceLevel)
	}), nil
}

func (e EVM) FilterLogs(ctx context.Context, meta capabilities.RequestMetadata, req *evm.FilterLogsRequest) (*capabilities.ResponseAndMetadata[*evm.FilterLogsReply], error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	ethFilterQuery, err := evm.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, err
	}

	request, err := e.filterLogsToRequest(meta, ethFilterQuery)
	if err != nil {
		return nil, err
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsInitiated(telemetryContext, ethFilterQuery))

	var reply evm.FilterLogsReply
	err = e.readProto(ctx, request, &reply)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsError(telemetryContext, ethFilterQuery, "Failed to FilterLogs", err.Error()))
		return nil, err
	}

	// G115: integer overflow conversion int -> int32 (gosec)
	// nolint:gosec
	monitoring.LogAndEmitSuccess(ctx, "Successfully executed FilterLogs", e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsSuccess(telemetryContext, ethFilterQuery, int32(len(reply.Logs))))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.FilterLogsReply]{
		Response:         &reply,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e EVM) BalanceAt(ctx context.Context, meta capabilities.RequestMetadata, req *evm.BalanceAtRequest) (*capabilities.ResponseAndMetadata[*evm.BalanceAtReply], error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, err
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildBalanceAtInitiated(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64()))

	balanceAt := func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
		callBlockNumber, err := getCallBlockNumber(blockNumber, height)
		if err != nil {
			return nil, fmt.Errorf("error getting call block number: %w", err)
		}
		reply, err := e.EVMService.BalanceAt(ctx, evmtypes.BalanceAtRequest{
			Address:         evmtypes.Address(req.GetAccount()),
			BlockNumber:     callBlockNumber,
			ConfidenceLevel: confidenceLevel,
		})
		if err != nil {
			return nil, err
		}

		pbBalance := valuespb.NewBigIntFromInt(reply.Balance)
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
		errMsg := e.messageBuilder.BuildBalanceAtError(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), "Failed to read BalanceAt", err.Error())
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, errMsg)
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read BalanceAt", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildBalanceAtSuccess(telemetryContext, common.Bytes2Hex(req.GetAccount()), blockNumber.Int64(), valuespb.NewIntFromBigInt(balance)))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.BalanceAtReply]{
		Response:         &evm.BalanceAtReply{Balance: balance},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e EVM) EstimateGas(ctx context.Context, meta capabilities.RequestMetadata, req *evm.EstimateGasRequest) (*capabilities.ResponseAndMetadata[*evm.EstimateGasReply], error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	msg, err := evm.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, err
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildEstimateGasInitiated(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data))

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
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildEstimateGasError(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, "Failed to execute EstimateGas", err.Error()))
		return nil, err
	}

	logMsg := e.messageBuilder.BuildEstimateGasSuccess(telemetryContext, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, rawEstimate.BigInt().Int64())
	monitoring.LogAndEmitSuccess(ctx, "Successfully read EstimateGas", e.lggr, e.beholderProcessor, logMsg)
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.EstimateGasReply]{
		Response:         &evm.EstimateGasReply{Gas: rawEstimate.BigInt().Uint64()},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e EVM) GetTransactionByHash(ctx context.Context, meta capabilities.RequestMetadata, req *evm.GetTransactionByHashRequest) (*capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply], error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := evm.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionByHashInitiated(telemetryContext, common.Bytes2Hex(hash[:])))
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		tx, err := e.EVMService.GetTransactionByHash(ctx, evmtypes.GetTransactionByHashRequest{
			Hash:       hash,
			IsExternal: true,
		})
		if err != nil {
			return nil, err
		}

		protoTx, err := evm.ConvertTransactionToProto(tx)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoTx)
	})

	var tx evm.Transaction
	if err := e.readProto(ctx, request, &tx); err != nil {
		errMsg := e.messageBuilder.BuildGetTransactionByHashError(telemetryContext, common.Bytes2Hex(hash[:]), "Failed to execute GetTransactionByHash", err.Error())
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, errMsg)
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionByHash", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionByHashSuccess(telemetryContext, common.Bytes2Hex(hash[:]), &tx))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.GetTransactionByHashReply]{
		Response:         &evm.GetTransactionByHashReply{Transaction: &tx},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e EVM) GetTransactionReceipt(ctx context.Context, meta capabilities.RequestMetadata, req *evm.GetTransactionReceiptRequest) (*capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply], error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	hash, err := evm.ConvertHashFromProto(req.GetHash())
	if err != nil {
		return nil, err
	}
	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptInitiated(telemetryContext, common.Bytes2Hex(hash[:])))
	request := ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
		receipt, err := e.EVMService.GetTransactionReceipt(ctx, evmtypes.GeTransactionReceiptRequest{
			Hash:       hash,
			IsExternal: true,
		})
		if err != nil {
			return nil, err
		}

		protoReceipt, err := evm.ConvertReceiptToProto(receipt)
		if err != nil {
			return nil, err
		}

		return proto.MarshalOptions{Deterministic: true}.Marshal(protoReceipt)
	})

	var receipt evm.Receipt
	if err := e.readProto(ctx, request, &receipt); err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptError(telemetryContext, common.Bytes2Hex(hash[:]), "Failed to get latest and finalized head", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionReceiptSuccess", e.lggr, e.beholderProcessor,
		e.messageBuilder.BuildGetTransactionReceiptSuccess(telemetryContext, common.Bytes2Hex(hash[:]), &receipt))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.GetTransactionReceiptReply]{
		Response:         &evm.GetTransactionReceiptReply{Receipt: &receipt},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e EVM) HeaderByNumber(
	ctx context.Context,
	meta capabilities.RequestMetadata,
	req *evm.HeaderByNumberRequest,
) (*capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply], error) {
	telemetryContext := monitoring.TelemetryContext{TsStart: time.Now().UnixMilli(), RequestMetadata: meta}
	blockNumber, needsBlockHeightConsensus, confidenceLevel, err := normalizeBlockNumber(req.GetBlockNumber())
	if err != nil {
		return nil, err
	}

	monitoring.EmitInitiated(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildHeaderByNumberInitiated(telemetryContext, blockNumber.Int64()))

	headerByNumber := func(ctx context.Context, blockNumber *big.Int) ([]byte, error) {
		reply, err := e.EVMService.HeaderByNumber(ctx, evmtypes.HeaderByNumberRequest{
			Number:          blockNumber,
			ConfidenceLevel: confidenceLevel,
		})
		if err != nil {
			return nil, err
		}

		if reply.Header == nil {
			return nil, fmt.Errorf("header is nil")
		}

		return proto.Marshal(&evm.HeaderByNumberReply{Header: evm.ConvertHeaderToProto(*reply.Header)})
	}

	var request ctypes.Request
	if needsBlockHeightConsensus {
		request = ctypes.NewLockableToBlockRequest(requestID(meta), func(ctx context.Context, height *ctypes.ChainHeight) ([]byte, error) {
			callBlockNumber, err := getCallBlockNumber(blockNumber, height)
			if err != nil {
				return nil, fmt.Errorf("error getting call block number: %w", err)
			}

			return headerByNumber(ctx, callBlockNumber)
		})
	} else {
		request = ctypes.NewEventuallyConsistentRequest(requestID(meta), func(ctx context.Context) ([]byte, error) {
			return headerByNumber(ctx, big.NewInt(blockNumber.Int64()))
		})
	}

	var reply evm.HeaderByNumberReply
	err = e.readProto(ctx, request, &reply)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildHeaderByNumberError(telemetryContext, blockNumber.Int64(), "Failed to get header by number", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully got header by number", e.lggr, e.beholderProcessor, e.messageBuilder.BuildHeaderByNumberSuccess(telemetryContext, blockNumber.Int64(), reply.Header))
	responseAndMetadata := capabilities.ResponseAndMetadata[*evm.HeaderByNumberReply]{
		Response:         &reply,
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, nil
}

func (e EVM) RegisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *evm.RegisterLogTrackingRequest) (*capabilities.ResponseAndMetadata[*emptypb.Empty], error) {
	filter, err := evm.ConvertLPFilterFromProto(req.GetFilter())
	if err != nil {
		return nil, err
	}
	responseAndMetadata := capabilities.ResponseAndMetadata[*emptypb.Empty]{
		Response:         &emptypb.Empty{},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, e.EVMService.RegisterLogTracking(etx, filter)
}

func (e EVM) UnregisterLogTracking(etx context.Context, _ capabilities.RequestMetadata, req *evm.UnregisterLogTrackingRequest) (*capabilities.ResponseAndMetadata[*emptypb.Empty], error) {
	responseAndMetadata := capabilities.ResponseAndMetadata[*emptypb.Empty]{
		Response:         &emptypb.Empty{},
		ResponseMetadata: capabilities.ResponseMetadata{},
	}
	return &responseAndMetadata, e.EVMService.UnregisterLogTracking(etx, req.FilterName)
}

// normalizeBlockNumber - returns:
// number - normalized block number converted to a corresponding tag, if possible
// needsBlockHeightConsensus - true, if DON Nodes need to agree on common height for corresponding tag, before agreeing on request reply.
func normalizeBlockNumber(pbBlockNumber *valuespb.BigInt) (number rpc.BlockNumber, needsBlockHeightConsensus bool, confidenceLevel primitives.ConfidenceLevel, err error) {
	// Replicate EthClient API, that treats nil block number as latest
	if pbBlockNumber == nil {
		return rpc.LatestBlockNumber, true, primitives.Unconfirmed, nil
	}

	bigBlockNumber := valuespb.NewIntFromBigInt(pbBlockNumber)
	if !bigBlockNumber.IsInt64() {
		return 0, false, primitives.Unconfirmed, fmt.Errorf("block number %s is not an int64", bigBlockNumber)
	}

	blockNumber := rpc.BlockNumber(bigBlockNumber.Int64())
	if blockNumber > 0 {
		return blockNumber, false, primitives.Unconfirmed, nil
	}

	switch blockNumber {
	case rpc.SafeBlockNumber:
		confidenceLevel = primitives.Safe
	case rpc.FinalizedBlockNumber:
		confidenceLevel = primitives.Finalized
	case rpc.LatestBlockNumber:
		confidenceLevel = primitives.Unconfirmed
	default:
		return 0, false, primitives.Unconfirmed, fmt.Errorf("block number %d is not supported", blockNumber)
	}

	return blockNumber, true, confidenceLevel, nil
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
	case reply := <-resultCh:
		if reply.Err != nil {
			return zero, reply.Err
		}
		data, ok := reply.Value.(T)
		if !ok {
			return zero, fmt.Errorf("unexpected result type: expected %T, got %T", zero, reply.Value)
		}

		return data, nil
	}
}
