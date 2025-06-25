package actions

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"google.golang.org/protobuf/types/known/emptypb"

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

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildCallContractInitiated(read, callMsg, bn)); err != nil {
		e.lggr.Errorw("CallContractInitiated failed", "err", err)
	}

	data, err := e.EVMService.CallContract(ctx, callMsg, bn)
	if err != nil {
		e.lggr.Errorw("CallContract failed", "contract", callMsg.To, "block", bn.String(), "err", err)
		err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildCallContractError(read, callMsg, bn, "call error", err.Error()))
		if err2 != nil {
			return nil, fmt.Errorf("post-error proc failed: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	e.lggr.Infow("CallContract success", "contract", callMsg.To, "block", bn.String())
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildCallContractSuccess(read, callMsg, bn)); err != nil {
		e.lggr.Errorw("CallContractSuccess proc failed", "err", err)
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

	// Initiated
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildFilterLogsInitiated(read, fq.FromBlock, fq.ToBlock)); err != nil {
		e.lggr.Errorw("FilterLogsInitiated failed", "err", err)
	}

	// Execute
	logs, err := e.EVMService.FilterLogs(ctx, fq)
	if err != nil {
		e.lggr.Errorw("FilterLogs failed", "from", fq.FromBlock, "to", fq.ToBlock, "err", err)
		err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildFilterLogsError(read, fq.FromBlock, fq.ToBlock, "filter error", err.Error()))
		if err2 != nil {
			return nil, fmt.Errorf("post-error proc failed: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	// Success
	e.lggr.Infow("FilterLogs success", "count", len(logs))
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildFilterLogsSuccess(read, fq.FromBlock, fq.ToBlock, len(logs))); err != nil {
		e.lggr.Errorw("FilterLogsSuccess proc failed", "err", err)
	}

	return &evmservice.FilterLogsReply{Logs: evmservice.ConvertLogsToProto(logs)}, nil
}

// BalanceAt with metrics/logs
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

	// Initiated
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildBalanceAtInitiated(read, common.Bytes2Hex(input.GetAccount()), bn)); err != nil {
		e.lggr.Errorw("BalanceAtInitiated failed", "err", err)
	}

	// Execute
	bal, err := e.EVMService.BalanceAt(ctx, evmtypes.Address(input.GetAccount()), bn)
	if err != nil {
		e.lggr.Errorw("BalanceAt failed", "account", input.GetAccount(), "err", err)
		err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildBalanceAtError(read, common.Bytes2Hex(input.GetAccount()), bn, "balance error", err.Error()))
		if err2 != nil {
			return nil, fmt.Errorf("post-error proc failed: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	// Success
	e.lggr.Infow("BalanceAt success", "account", input.GetAccount(), "balance", bal.String())
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildBalanceAtSuccess(read, common.Bytes2Hex(input.GetAccount()), bn, bal)); err != nil {
		e.lggr.Errorw("BalanceAtSuccess proc failed", "err", err)
	}

	return &evmservice.BalanceAtReply{Balance: pb.NewBigIntFromInt(bal)}, nil
}

// EstimateGas with metrics/logs
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

	// Initiated
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildEstimateGasInitiated(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data)); err != nil {
		e.lggr.Errorw("EstimateGasInitiated failed", "err", err)
	}

	// Execute
	estimate, err := e.EVMService.EstimateGas(ctx, msg)
	if err != nil {
		e.lggr.Errorw("EstimateGas failed", "err", err)
		err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildEstimateGasError(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, "estimate error", err.Error()))
		if err2 != nil {
			return nil, fmt.Errorf("post-error proc failed: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	// Success
	e.lggr.Infow("EstimateGas success", "gas", estimate)
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildEstimateGasSuccess(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, estimate)); err != nil {
		e.lggr.Errorw("EstimateGasSuccess proc failed", "err", err)
	}

	return &evmservice.EstimateGasReply{Gas: estimate}, nil
}

// GetTransactionByHash with metrics/logs
func (e EVM) GetTransactionByHash(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.GetTransactionByHashRequest,
) (*evmservice.GetTransactionByHashReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	// Initiated
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionByHashInitiated(read, common.Bytes2Hex(input.GetHash()))); err != nil {
		e.lggr.Errorw("GetTxByHashInitiated failed", "err", err)
	}

	// Execute
	tx, err := e.EVMService.GetTransactionByHash(ctx, evmtypes.Hash(input.GetHash()))
	if err != nil {
		e.lggr.Errorw("GetTxByHash failed", "hash", input.GetHash(), "err", err)
		err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionByHashError(read, common.Bytes2Hex(input.GetHash()), "get tx error", err.Error()))
		if err2 != nil {
			return nil, fmt.Errorf("post-error proc failed: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	// Convert
	protoTx, err := evmservice.ConvertTransactionToProto(tx)
	if err != nil {
		return nil, err
	}

	// Success
	e.lggr.Infow("GetTxByHash success", "hash", input.GetHash())
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionByHashSuccess(read, common.Bytes2Hex(input.GetHash()), tx)); err != nil {
		e.lggr.Errorw("GetTxByHashSuccess proc failed", "err", err)
	}

	return &evmservice.GetTransactionByHashReply{Transaction: protoTx}, nil
}

// GetTransactionReceipt with metrics/logs
func (e EVM) GetTransactionReceipt(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmservice.GetTransactionReceiptRequest,
) (*evmservice.GetTransactionReceiptReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	// Initiated
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionReceiptInitiated(read, common.Bytes2Hex(input.GetHash()))); err != nil {
		e.lggr.Errorw("GetReceiptInitiated failed", "err", err)
	}

	// Execute
	rcp, err := e.EVMService.GetTransactionReceipt(ctx, evmtypes.Hash(input.GetHash()))
	if err != nil {
		e.lggr.Errorw("GetReceipt failed", "hash", input.GetHash(), "err", err)
		err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionReceiptError(read, common.Bytes2Hex(input.GetHash()), "receipt error", err.Error()))
		if err2 != nil {
			return nil, fmt.Errorf("post-error proc failed: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	// Convert
	protoR, err := evmservice.ConvertReceiptToProto(rcp)
	if err != nil {
		return nil, err
	}

	// Success
	e.lggr.Infow("GetReceipt success", "hash", input.GetHash(), "status", protoR.GetStatus())
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionReceiptSuccess(read, common.Bytes2Hex(input.GetHash()), rcp)); err != nil {
		e.lggr.Errorw("GetReceiptSuccess proc failed", "err", err)
	}

	return &evmservice.GetTransactionReceiptReply{Receipt: protoR}, nil
}

// LatestAndFinalizedHead with metrics/logs
func (e EVM) LatestAndFinalizedHead(
	ctx context.Context,
	req capabilities.RequestMetadata,
	_ *emptypb.Empty,
) (*evmservice.LatestAndFinalizedHeadReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	// Initiated
	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildLatestAndFinalizedHeadInitiated(read)); err != nil {
		e.lggr.Errorw("HeadInitiated failed", "err", err)
	}

	// Execute
	latest, fin, err := e.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		e.lggr.Errorw("Head failed", "err", err)
		err2 := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildLatestAndFinalizedHeadError(read, "head error", err.Error()))
		if err2 != nil {
			return nil, fmt.Errorf("post-error proc failed: %w (orig: %w)", err2, err)
		}
		return nil, err
	}

	e.lggr.Infow("Head success", "latest", latest.Number, "finalized", fin.Number)
	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildLatestAndFinalizedHeadSuccess(read, latest, fin)); err != nil {
		e.lggr.Errorw("HeadSuccess proc failed", "err", err)
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
