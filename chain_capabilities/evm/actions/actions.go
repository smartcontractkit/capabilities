package actions

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	chaincommonpb "github.com/smartcontractkit/chainlink-common/pkg/loop/chain-common"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type EVM struct {
	types.EVMService
	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder
}

func NewEVM(evmService types.EVMService, lggr logger.Logger, beholderProcessor beholder.ProtoProcessor, messageBuilder *monitoring.MessageBuilder) EVM {
	return EVM{
		EVMService:        evmService,
		lggr:              lggr,
		beholderProcessor: beholderProcessor,
		messageBuilder:    messageBuilder,
	}
}

func (e EVM) CallContract(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.CallContractRequest,
) (*evmservice.CallContractReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	callMsg, err := evmservice.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, err
	}
	bn := pb.NewIntFromBigInt(input.GetBlockNumber())
	if bn == nil || bn.Int64() == 0 {
		return nil, fmt.Errorf("block number must be non-zero, got %s", bn)
	}

	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildCallContractInitiated(read, callMsg, bn)); err != nil {
		e.lggr.Errorw("failed to process CallContractInitiated message", "err", err)
	}

	data, err := e.EVMService.CallContract(ctx, callMsg, bn)
	if err != nil {
		e.lggr.Errorw("failed to read CallContract", "contract", callMsg.To, "block", bn.String(), "err", err)
		if err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildCallContractError(read, callMsg, bn, "failed to execute CallContract", err.Error())); err2 != nil {
			return nil, fmt.Errorf("failed to process CallContractError message: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	e.lggr.Infow("Successfully read CallContract", "contract", callMsg.To, "block", bn.String())
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildCallContractSuccess(read, callMsg, bn)); err != nil {
		e.lggr.Errorw("failed to process CallContractSuccess message", "err", err)
	}

	return &evmservice.CallContractReply{Data: data}, nil
}

func (e EVM) FilterLogs(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.FilterLogsRequest,
) (*evmservice.FilterLogsReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	fq, err := evmservice.ConvertFilterFromProto(input.GetFilterQuery())
	if err != nil {
		return nil, err
	}
	if fq.FromBlock == nil || fq.ToBlock == nil || fq.FromBlock.Cmp(fq.ToBlock) > 0 {
		return nil, fmt.Errorf("invalid range: %s-%s", fq.FromBlock, fq.ToBlock)
	}

	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildFilterLogsInitiated(read, fq.FromBlock, fq.ToBlock)); err != nil {
		e.lggr.Errorw("failed to process FilterLogsInitiated message", "err", err)
	}

	logs, err := e.EVMService.FilterLogs(ctx, fq)
	if err != nil {
		e.lggr.Errorw("failed to execute FilterLogs", "from", fq.FromBlock, "to", fq.ToBlock, "err", err)
		if err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildFilterLogsError(read, fq.FromBlock, fq.ToBlock, "failed to execute FilterLogs", err.Error())); err2 != nil {
			return nil, fmt.Errorf("failed to process FilterLogsError message: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	e.lggr.Infow("successfully executed FilterLogs", "count", len(logs))
	// G115: integer overflow conversion int -> int32 (gosec)
	// nolint:gosec
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildFilterLogsSuccess(read, fq.FromBlock, fq.ToBlock, int32(len(logs)))); err != nil {
		e.lggr.Errorw("failed to process FilterLogsSuccess message", "err", err)
	}

	return &evmservice.FilterLogsReply{Logs: evmservice.ConvertLogsToProto(logs)}, nil
}

func (e EVM) BalanceAt(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.BalanceAtRequest,
) (*evmservice.BalanceAtReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	bn := pb.NewIntFromBigInt(input.GetBlockNumber())
	if bn == nil || bn.Int64() == 0 {
		return nil, fmt.Errorf("invalid block number %s", bn)
	}

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildBalanceAtInitiated(read, common.Bytes2Hex(input.GetAccount()), bn)); err != nil {
		e.lggr.Errorw("Failed to process BalanceAtInitiated message", "err", err)
	}

	bal, err := e.EVMService.BalanceAt(ctx, evmtypes.Address(input.GetAccount()), bn)
	if err != nil {
		e.lggr.Errorw("Failed to execute BalanceAt", "account", input.GetAccount(), "err", err)
		if err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildBalanceAtError(read, common.Bytes2Hex(input.GetAccount()), bn, "failed to execute BalanceAt", err.Error())); err2 != nil {
			return nil, fmt.Errorf("failed to process BalanceAtError message: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	e.lggr.Infow("Successfully executed BalanceAt", "account", input.GetAccount(), "balance", bal.String())
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildBalanceAtSuccess(read, common.Bytes2Hex(input.GetAccount()), bn, bal)); err != nil {
		e.lggr.Errorw("Failed to process BalanceAtSuccess message", "err", err)
	}

	return &evmservice.BalanceAtReply{Balance: pb.NewBigIntFromInt(bal)}, nil
}

func (e EVM) EstimateGas(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.EstimateGasRequest,
) (*evmservice.EstimateGasReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	msg, err := evmservice.ConvertCallMsgFromProto(input.GetMsg())
	if err != nil {
		return nil, err
	}

	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildEstimateGasInitiated(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data)); err != nil {
		e.lggr.Errorw("Failed to process EstimateGasInitiated message", "err", err)
	}

	estimate, err := e.EVMService.EstimateGas(ctx, msg)
	if err != nil {
		e.lggr.Errorw("Failed to execute EstimateGas", "err", err)
		if err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildEstimateGasError(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, "failed to execute EstimateGas", err.Error())); err2 != nil {
			return nil, fmt.Errorf("failed to process EstimateGasError message: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	e.lggr.Infow("Successfully executed EstimateGas", "gas", estimate)

	//  G115: integer overflow conversion uint64 -> int64 (gosec)
	// nolint:gosec
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildEstimateGasSuccess(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, int64(estimate))); err != nil {
		e.lggr.Errorw("Failed to process EstimateGasSuccess message", "err", err)
	}

	return &evmservice.EstimateGasReply{Gas: estimate}, nil
}

func (e EVM) GetTransactionByHash(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.GetTransactionByHashRequest,
) (*evmservice.GetTransactionByHashReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionByHashInitiated(read, common.Bytes2Hex(input.GetHash()))); err != nil {
		e.lggr.Errorw("Failed to process GetTransactionByHashInitiated message", "err", err)
	}

	tx, err := e.EVMService.GetTransactionByHash(ctx, evmtypes.Hash(input.GetHash()))
	if err != nil {
		e.lggr.Errorw("Failed to execute GetTransactionByHash", "hash", input.GetHash(), "err", err)
		if err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionByHashError(read, common.Bytes2Hex(input.GetHash()), "failed to execute GetTransactionByHash", err.Error())); err2 != nil {
			return nil, fmt.Errorf("failed to process GetTransactionByHashError message: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	protoTx, err := evmservice.ConvertTransactionToProto(tx)
	if err != nil {
		return nil, err
	}

	e.lggr.Infow("Successfully executed GetTransactionByHash", "hash", input.GetHash())
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionByHashSuccess(read, common.Bytes2Hex(input.GetHash()), tx)); err != nil {
		e.lggr.Errorw("Failed to process GetTransactionByHashSuccess message", "err", err)
	}

	return &evmservice.GetTransactionByHashReply{Transaction: protoTx}, nil
}

func (e EVM) GetTransactionReceipt(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.GetTransactionReceiptRequest,
) (*evmservice.GetTransactionReceiptReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionReceiptInitiated(read, common.Bytes2Hex(input.GetHash()))); err != nil {
		e.lggr.Errorw("Failed to process GetTransactionReceiptInitiated message", "err", err)
	}

	rcp, err := e.EVMService.GetTransactionReceipt(ctx, evmtypes.Hash(input.GetHash()))
	if err != nil {
		e.lggr.Errorw("Failed to execute GetTransactionReceipt", "hash", input.GetHash(), "err", err)
		if err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionReceiptError(read, common.Bytes2Hex(input.GetHash()), "failed to execute GetTransactionReceipt", err.Error())); err2 != nil {
			return nil, fmt.Errorf("failed to process GetTransactionReceiptError message: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	protoR, err := evmservice.ConvertReceiptToProto(rcp)
	if err != nil {
		return nil, err
	}

	e.lggr.Infow("Successfully read tx receipt", "hash", input.GetHash(), "status", protoR.GetStatus())
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionReceiptSuccess(read, common.Bytes2Hex(input.GetHash()), rcp)); err != nil {
		e.lggr.Errorw("Failed to process GetTransactionReceiptSuccess", "err", err)
	}

	return &evmservice.GetTransactionReceiptReply{Receipt: protoR}, nil
}

// TODO turn this into GetBlock(... finality...)
func (e EVM) LatestAndFinalizedHead(
	ctx context.Context,
	req capabilities.RequestMetadata,
	_ *emptypb.Empty,
) (*evmservice.LatestAndFinalizedHeadReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildLatestAndFinalizedHeadInitiated(read)); err != nil {
		e.lggr.Errorw("Failed to process LatestAndFinalizedHeadInitiated message", "err", err)
	}

	latest, fin, err := e.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		e.lggr.Errorw("Failed to get latest and finalized head", "err", err)
		if err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildLatestAndFinalizedHeadError(read, "failed to get latest and finalized head", err.Error())); err2 != nil {
			return nil, fmt.Errorf("failed to process LatestAndFinalizedHeadError message: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	e.lggr.Infow("Successfully read latest and finalize head", "latest", latest.Number, "finalized", fin.Number)
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildLatestAndFinalizedHeadSuccess(read, latest, fin)); err != nil {
		e.lggr.Errorw("Failed to process LatestAndFinalizedHeadSuccess message", "err", err)
	}

	return &evmservice.LatestAndFinalizedHeadReply{Latest: evmservice.ConvertHeadToProto(latest), Finalized: evmservice.ConvertHeadToProto(fin)}, nil
}

// TODO remove
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
