package actions

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmservice "github.com/smartcontractkit/chainlink-common/pkg/chains/evm"
	chaincommonpb "github.com/smartcontractkit/chainlink-common/pkg/loop/chain-common"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type EVM struct {
	types.EVMService
}

func NewEVM(evmService types.EVMService) EVM {
	return EVM{EVMService: evmService}
}

// TODO finalise the signature PLEX-1482
func (e EVM) CallContract(etx context.Context, _ capabilities.RequestMetadata, input *evmservice.CallContractRequest) (*evmservice.CallContractReply, error) {
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

	data, err := e.EVMService.CallContract(etx, callMsg, blockNumber)
	if err != nil {
		return nil, err
	}

	return &evmservice.CallContractReply{Data: data}, nil
}

func (e EVM) FilterLogs(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.FilterLogsRequest) (*evmservice.FilterLogsReply, error) {
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

	logs, err := e.EVMService.FilterLogs(etx, fq)
	if err != nil {
		return nil, err
	}

	return &evmservice.FilterLogsReply{Logs: evmservice.ConvertLogsToProto(logs)}, nil
}

func (e EVM) BalanceAt(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.BalanceAtRequest) (*evmservice.BalanceAtReply, error) {
	blockNumber := valuespb.NewIntFromBigInt(req.GetBlockNumber())

	// TODO allow the block number to be nil or zero, which would default to the latest block, this requires an OCR round to reach consesnus on balance read.
	// To do this the EVMService has to be modified/extended to return from which block the balance was read or even a list of balances from a couple of blocks.
	// alternatively, just return median of the balance value?
	if blockNumber == nil || blockNumber.String() == "0" {
		return nil, fmt.Errorf("block number must be specified and non-zero, got: %s", blockNumber)
	}

	balance, err := e.EVMService.BalanceAt(etx, evmtypes.Address(req.GetAccount()), blockNumber)
	if err != nil {
		return nil, err
	}

	return &evmservice.BalanceAtReply{Balance: valuespb.NewBigIntFromInt(balance)}, nil
}

func (e EVM) EstimateGas(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.EstimateGasRequest) (*evmservice.EstimateGasReply, error) {
	// TODO double check if an ocr round can just return a median of the estimate gas value and implement this.
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

func (e EVM) GetTransactionByHash(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.GetTransactionByHashRequest) (*evmservice.GetTransactionByHashReply, error) {
	tx, err := e.EVMService.GetTransactionByHash(etx, evmtypes.Hash(req.Hash))
	if err != nil {
		return nil, err
	}

	protoTx, err := evmservice.ConvertTransactionToProto(tx)
	if err != nil {
		return nil, err
	}

	return &evmservice.GetTransactionByHashReply{Transaction: protoTx}, nil
}

func (e EVM) GetTransactionReceipt(etx context.Context, _ capabilities.RequestMetadata, req *evmservice.GetTransactionReceiptRequest) (*evmservice.GetTransactionReceiptReply, error) {
	receipt, err := e.EVMService.GetTransactionReceipt(etx, evmtypes.Hash(req.Hash))
	if err != nil {
		return nil, err
	}

	protoReceipt, err := evmservice.ConvertReceiptToProto(receipt)
	if err != nil {
		return nil, err
	}

	return &evmservice.GetTransactionReceiptReply{Receipt: protoReceipt}, nil
}

func (e EVM) LatestAndFinalizedHead(etx context.Context, _ capabilities.RequestMetadata, _ *emptypb.Empty) (*evmservice.LatestAndFinalizedHeadReply, error) {
	// TODO need to start an OCR round here to get median of latest and finalized head, do we need a list of blocks to do this or can we get the DON median from the OCRFactory?
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
