package actions

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/contracts"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

type EVM struct {
	types.EVMService
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

// TODO finalise the signature PLEX-1482
func (e EVM) CallContract(ctx context.Context, _ capabilities.RequestMetadata, input *evmcappb.CallContractRequest) (*evmcappb.CallContractReply, error) {
	callMsg, err := evmcappb.ConvertCallMsgFromProto(input.GetCall())
	if err != nil {
		return nil, err
	}

	// TODO handle unspecified block number, which defaults to the latest block, need an OCR round for consensus on read.
	// To do this the EVMService has to be modified/extended to return from which block the contract was read or even a list of contact reads from a couple of blocks.
	blockNumber := valuespb.NewIntFromBigInt(input.GetBlockNumber())
	if blockNumber == nil || blockNumber.String() == "0" {
		return nil, fmt.Errorf("block number must be specified and non-zero, got: %s", blockNumber)
	}

	data, err := e.EVMService.CallContract(ctx, callMsg, blockNumber)
	if err != nil {
		return nil, err
	}

	return &evmcappb.CallContractReply{Data: data}, nil
}

func (e EVM) FilterLogs(ctx context.Context, _ capabilities.RequestMetadata, req *evmcappb.FilterLogsRequest) (*evmcappb.FilterLogsReply, error) {
	fq, err := evmcappb.ConvertFilterFromProto(req.GetFilterQuery())
	if err != nil {
		return nil, err
	}

	if (fq.FromBlock == nil || fq.FromBlock.String() == "0") || (fq.ToBlock == nil || fq.ToBlock.String() == "0") {
		return nil, fmt.Errorf("fromBlock and toBlock have to be specified and bounded in the filter query, got: fromBlock=%s, toBlock=%s", fq.FromBlock, fq.ToBlock)
	}

	if fq.FromBlock.Cmp(fq.ToBlock) > 0 {
		return nil, fmt.Errorf("fromBlock (%s) cannot be greater than toBlock (%s)", fq.FromBlock, fq.ToBlock)
	}

	logs, err := e.EVMService.FilterLogs(ctx, fq)
	if err != nil {
		return nil, err
	}

	return &evmcappb.FilterLogsReply{Logs: evmcappb.ConvertLogsToProto(logs)}, nil
}

func (e EVM) BalanceAt(etx context.Context, _ capabilities.RequestMetadata, req *evmcappb.BalanceAtRequest) (*evmcappb.BalanceAtReply, error) {
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

	return &evmcappb.BalanceAtReply{Balance: valuespb.NewBigIntFromInt(balance)}, nil
}

func (e EVM) EstimateGas(etx context.Context, _ capabilities.RequestMetadata, req *evmcappb.EstimateGasRequest) (*evmcappb.EstimateGasReply, error) {
	// TODO double check if an ocr round can just return a median of the estimate gas value and implement this.
	callMsg, err := evmcappb.ConvertCallMsgFromProto(req.GetMsg())
	if err != nil {
		return nil, err
	}

	estimate, err := e.EVMService.EstimateGas(etx, callMsg)
	if err != nil {
		return &evmcappb.EstimateGasReply{}, err
	}

	return &evmcappb.EstimateGasReply{Gas: estimate}, nil
}

func (e EVM) GetTransactionByHash(etx context.Context, _ capabilities.RequestMetadata, req *evmcappb.GetTransactionByHashRequest) (*evmcappb.GetTransactionByHashReply, error) {
	tx, err := e.EVMService.GetTransactionByHash(etx, evmtypes.Hash(req.Hash))
	if err != nil {
		return nil, err
	}

	protoTx, err := evmcappb.ConvertTransactionToProto(tx)
	if err != nil {
		return nil, err
	}

	return &evmcappb.GetTransactionByHashReply{Transaction: protoTx}, nil
}

func (e EVM) GetTransactionReceipt(etx context.Context, _ capabilities.RequestMetadata, req *evmcappb.GetTransactionReceiptRequest) (*evmcappb.GetTransactionReceiptReply, error) {
	receipt, err := e.EVMService.GetTransactionReceipt(etx, evmtypes.Hash(req.Hash))
	if err != nil {
		return nil, err
	}

	protoReceipt, err := evmcappb.ConvertReceiptToProto(receipt)
	if err != nil {
		return nil, err
	}

	return &evmcappb.GetTransactionReceiptReply{Receipt: protoReceipt}, nil
}

func (e EVM) LatestAndFinalizedHead(etx context.Context, _ capabilities.RequestMetadata, _ *emptypb.Empty) (*evmcappb.LatestAndFinalizedHeadReply, error) {
	// TODO need to start an OCR round here to get median of latest and finalized head, do we need a list of blocks to do this or can we get the DON median from the OCRFactory?
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
