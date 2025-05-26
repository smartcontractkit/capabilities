package evm

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm/chain-service"
	chaincommonpb "github.com/smartcontractkit/chainlink-common/pkg/loop/chain-common"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

func (c *capability) CallContract(ctx context.Context, _ capabilities.RequestMetadata, input *evmservice.CallContractRequest) (*evmservice.CallContractReply, error) {
	callMsg, err := evmservice.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, err
	}

	// TODO handle unspecified block number, which defaults to the latest block, need an OCR round for consensus on read.
	// To do this the EVMService has to be modified/extended to return from which block the contract was read or even a list of contact reads from a couple of blocks.
	blockNumber := valuespb.NewIntFromBigInt(input.GetBlockNumber())
	if blockNumber == nil || blockNumber.String() == "0" {
		return nil, fmt.Errorf("block number must be specified and non-zero, got: %s", blockNumber)
	}

	data, err := c.EVMService.CallContract(ctx, callMsg, blockNumber)
	if err != nil {
		return nil, err
	}

	return &evmservice.CallContractReply{Data: &evmservice.ABIPayload{Abi: data}}, nil
}

func (c *capability) FilterLogs(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.FilterLogsRequest) (*evmservice.FilterLogsReply, error) {
	fq, err := evmservice.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, err
	}

	if (fq.FromBlock == nil || fq.FromBlock.String() == "0") || (fq.ToBlock == nil || fq.ToBlock.String() == "0") {
		return nil, fmt.Errorf("fromBlock and toBlock have to be specified and bounded in the filter query, got: fromBlock=%s, toBlock=%s", fq.FromBlock, fq.ToBlock)
	}

	if fq.FromBlock.Cmp(fq.ToBlock) > 0 {
		return nil, fmt.Errorf("fromBlock (%s) cannot be greater than toBlock (%s)", fq.FromBlock, fq.ToBlock)
	}

	logs, err := c.EVMService.FilterLogs(ctx, fq)
	if err != nil {
		return nil, err
	}

	return &evmservice.FilterLogsReply{Logs: evmservice.ConvertLogsToProto(logs)}, nil
}

func (c *capability) BalanceAt(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.BalanceAtRequest) (*evmservice.BalanceAtReply, error) {
	blockNumber := valuespb.NewIntFromBigInt(req.GetBlockNumber())

	// TODO allow the block number to be nil or zero, which would default to the latest block, this requires an OCR round to reach consesnus on balance read.
	// To do this the EVMService has to be modified/extended to return from which block the balance was read or even a list of balances from a couple of blocks.
	// alternatively, just return median of the balance value?
	if blockNumber == nil || blockNumber.String() == "0" {
		return nil, fmt.Errorf("block number must be specified and non-zero, got: %s", blockNumber)
	}

	balance, err := c.EVMService.BalanceAt(ctx, evmtypes.Address(req.GetAccount().GetAddress()), blockNumber)
	if err != nil {
		return nil, err
	}

	return &evmservice.BalanceAtReply{Balance: valuespb.NewBigIntFromInt(balance)}, nil
}

func (c *capability) EstimateGas(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.EstimateGasRequest) (*evmservice.EstimateGasReply, error) {
	// TODO double check if an ocr round can just return a median of the estimate gas value and implement this.
	callMsg, err := evmservice.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, err
	}

	estimate, err := c.EVMService.EstimateGas(ctx, callMsg)
	if err != nil {
		return &evmservice.EstimateGasReply{}, err
	}

	return &evmservice.EstimateGasReply{Gas: estimate}, nil
}

func (c *capability) GetTransactionByHash(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.GetTransactionByHashRequest) (*evmservice.GetTransactionByHashReply, error) {
	tx, err := c.EVMService.GetTransactionByHash(ctx, evmtypes.Hash(req.Hash.Hash))
	if err != nil {
		return nil, err
	}

	protoTx, err := evmservice.ConvertTransactionToProto(tx)
	if err != nil {
		return nil, err
	}

	return &evmservice.GetTransactionByHashReply{Transaction: protoTx}, nil
}

func (c *capability) GetTransactionReceipt(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.GetTransactionReceiptRequest) (*evmservice.GetTransactionReceiptReply, error) {
	receipt, err := c.EVMService.GetTransactionReceipt(ctx, evmtypes.Hash(req.Hash.Hash))
	if err != nil {
		return nil, err
	}

	protoReceipt, err := evmservice.ConvertReceiptToProto(receipt)
	if err != nil {
		return nil, err
	}

	return &evmservice.GetTransactionReceiptReply{Receipt: protoReceipt}, nil
}

func (c *capability) LatestAndFinalizedHead(ctx context.Context, _ capabilities.RequestMetadata, _ *emptypb.Empty) (*evmservice.LatestAndFinalizedHeadReply, error) {
	// TODO need to start an OCR round here to get median of latest and finalized head, do we need a list of blocks to do this or can we get the DON median from the OCRFactory?
	latest, finalized, err := c.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		return nil, err
	}

	return &evmservice.LatestAndFinalizedHeadReply{
		Latest:    evmservice.ConvertHeadToProto(latest),
		Finalized: evmservice.ConvertHeadToProto(finalized),
	}, nil
}
func (c *capability) QueryTrackedLogs(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.QueryTrackedLogsRequest) (*evmservice.QueryTrackedLogsReply, error) {
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
	result, err := c.EVMService.QueryTrackedLogs(ctx, expression, limitAndSort, confidenceLevel)
	if err != nil {
		return nil, err
	}

	return &evmservice.QueryTrackedLogsReply{Logs: evmservice.ConvertLogsToProto(result)}, nil
}

func (c *capability) RegisterLogTracking(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.RegisterLogTrackingRequest) (*emptypb.Empty, error) {
	filter, err := evmservice.ConvertLPFilterFromProto(req.GetFilter())
	if err != nil {
		return nil, err
	}
	return &emptypb.Empty{}, c.EVMService.RegisterLogTracking(ctx, filter)
}

func (c *capability) UnregisterLogTracking(ctx context.Context, _ capabilities.RequestMetadata, req *evmservice.UnregisterLogTrackingRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, c.EVMService.UnregisterLogTracking(ctx, req.FilterName)
}
