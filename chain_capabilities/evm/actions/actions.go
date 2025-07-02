package actions

import (
	"context"
	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type EVM struct {
	types.EVMService
	keystoneForwarderAddress common.Address
	forwarderClient          contracts.CREForwarderClient
	ReceiverGasMinimum       uint64

	lggr              logger.Logger
	beholderProcessor beholder.ProtoProcessor
	messageBuilder    *monitoring.MessageBuilder
}

func NewEVM(cfg config.Config, evmService types.EVMService, lggr logger.Logger, beholderProcessor beholder.ProtoProcessor, messageBuilder *monitoring.MessageBuilder) (EVM, error) {
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
	}, nil
}

func (e EVM) CallContract(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmcappb.CallContractRequest,
) (*evmcappb.CallContractReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	callMsg, err := evmcappb.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, err
	}
	bn := pb.NewIntFromBigInt(input.GetBlockNumber())
	if bn == nil || bn.Int64() == 0 {
		return nil, fmt.Errorf("blockNumber must be non-zero, got %s", bn)
	}

	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildCallContractInitiated(read, callMsg, bn)); err != nil {
		e.lggr.Errorw("failed to process CallContractInitiated message", "err", err)
	}

	data, err := e.EVMService.CallContract(ctx, callMsg, bn)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractError(read, callMsg, bn, "Failed to read CallContract", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read CallContract", e.lggr, e.beholderProcessor, e.messageBuilder.BuildCallContractSuccess(read, callMsg, bn))
	return &evmcappb.CallContractReply{Data: data}, nil
}

func (e EVM) FilterLogs(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmcappb.FilterLogsRequest,
) (*evmcappb.FilterLogsReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	fq, err := evmcappb.ConvertFilterFromProto(input.GetFilterQuery())
	if err != nil {
		return nil, err
	}
	if fq.FromBlock == nil || fq.ToBlock == nil || fq.FromBlock.Cmp(fq.ToBlock) > 0 {
		return nil, fmt.Errorf("invalid range: %s-%s", fq.FromBlock, fq.ToBlock)
	}

	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildFilterLogsInitiated(read, fq)); err != nil {
		e.lggr.Errorw("failed to process FilterLogsInitiated message", "err", err)
	}

	logs, err := e.EVMService.FilterLogs(ctx, fq)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsError(read, fq, "Failed to FilterLogs", err.Error()))
		return nil, err
	}

	// G115: integer overflow conversion int -> int32 (gosec)
	// nolint:gosec
	monitoring.LogAndEmitSuccess(ctx, "Successfully executed FilterLogs", e.lggr, e.beholderProcessor, e.messageBuilder.BuildFilterLogsSuccess(read, fq, int32(len(logs))))
	return &evmcappb.FilterLogsReply{Logs: evmcappb.ConvertLogsToProto(logs)}, nil
}

func (e EVM) BalanceAt(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmcappb.BalanceAtRequest,
) (*evmcappb.BalanceAtReply, error) {
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
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildBalanceAtError(read, common.Bytes2Hex(input.GetAccount()), bn, "Failed to read BalanceAt", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read BalanceAt", e.lggr, e.beholderProcessor, e.messageBuilder.BuildBalanceAtSuccess(read, common.Bytes2Hex(input.GetAccount()), bn, bal))
	return &evmcappb.BalanceAtReply{Balance: pb.NewBigIntFromInt(bal)}, nil
}

func (e EVM) EstimateGas(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmcappb.EstimateGasRequest,
) (*evmcappb.EstimateGasReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	msg, err := evmcappb.ConvertCallMsgFromProto(input.GetMsg())
	if err != nil {
		return nil, err
	}

	if err = e.beholderProcessor.Process(ctx, e.messageBuilder.BuildEstimateGasInitiated(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data)); err != nil {
		e.lggr.Errorw("Failed to process EstimateGasInitiated message", "err", err)
	}

	estimate, err := e.EVMService.EstimateGas(ctx, msg)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildEstimateGasError(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, "Failed to execute EstimateGas", err.Error()))
		return nil, err
	}

	// G115: integer overflow conversion uint64 -> int64 (gosec)
	// nolint:gosec
	monitoring.LogAndEmitSuccess(ctx, "Successfully read EstimateGas", e.lggr, e.beholderProcessor, e.messageBuilder.BuildEstimateGasSuccess(read, common.Bytes2Hex(msg.From[:]), common.Bytes2Hex(msg.To[:]), msg.Data, int64(estimate)))
	return &evmcappb.EstimateGasReply{Gas: estimate}, nil
}

func (e EVM) GetTransactionByHash(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmcappb.GetTransactionByHashRequest,
) (*evmcappb.GetTransactionByHashReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionByHashInitiated(read, common.Bytes2Hex(input.GetHash()))); err != nil {
		e.lggr.Errorw("Failed to process GetTransactionByHashInitiated message", "err", err)
	}

	tx, err := e.EVMService.GetTransactionByHash(ctx, evmtypes.Hash(input.GetHash()))
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionByHashError(read, common.Bytes2Hex(input.GetHash()), "Failed to execute GetTransactionByHash", err.Error()))
		return nil, err
	}

	protoTx, err := evmcappb.ConvertTransactionToProto(tx)
	if err != nil {
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionByHash", e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionByHashSuccess(read, common.Bytes2Hex(input.GetHash()), tx))
	return &evmcappb.GetTransactionByHashReply{Transaction: protoTx}, nil
}

func (e EVM) GetTransactionReceipt(
	ctx context.Context,
	req capabilities.RequestMetadata,
	input *evmcappb.GetTransactionReceiptRequest,
) (*evmcappb.GetTransactionReceiptReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildGetTransactionReceiptInitiated(read, common.Bytes2Hex(input.GetHash()))); err != nil {
		e.lggr.Errorw("Failed to process GetTransactionReceiptInitiated message", "err", err)
	}

	rcp, err := e.EVMService.GetTransactionReceipt(ctx, evmtypes.Hash(input.GetHash()))
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptError(read, common.Bytes2Hex(input.GetHash()), "Failed to get latest and finalized head", err.Error()))
		return nil, err
	}

	protoR, err := evmcappb.ConvertReceiptToProto(rcp)
	if err != nil {
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read GetTransactionReceiptSuccess", e.lggr, e.beholderProcessor, e.messageBuilder.BuildGetTransactionReceiptSuccess(read, common.Bytes2Hex(input.GetHash()), rcp))
	return &evmcappb.GetTransactionReceiptReply{Receipt: protoR}, nil
}

// TODO turn this into GetBlock(... finality...)
func (e EVM) LatestAndFinalizedHead(
	ctx context.Context,
	req capabilities.RequestMetadata,
	_ *emptypb.Empty,
) (*evmcappb.LatestAndFinalizedHeadReply, error) {
	ts := time.Now().UnixMilli()
	read := monitoring.ReadRequest{TsStart: ts, RequestMetadata: req}

	if err := e.beholderProcessor.Process(ctx, e.messageBuilder.BuildLatestAndFinalizedHeadInitiated(read)); err != nil {
		e.lggr.Errorw("Failed to process LatestAndFinalizedHeadInitiated message", "err", err)
	}

	latest, fin, err := e.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		monitoring.LogAndEmitError(ctx, e.lggr, e.beholderProcessor, e.messageBuilder.BuildLatestAndFinalizedHeadError(read, "Failed to get latest and finalized head", err.Error()))
		return nil, err
	}

	monitoring.LogAndEmitSuccess(ctx, "Successfully read LatestAndFinalizedHead", e.lggr, e.beholderProcessor, e.messageBuilder.BuildLatestAndFinalizedHeadSuccess(read, latest, fin))
	return &evmcappb.LatestAndFinalizedHeadReply{Latest: evmcappb.ConvertHeadToProto(latest), Finalized: evmcappb.ConvertHeadToProto(fin)}, nil
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
