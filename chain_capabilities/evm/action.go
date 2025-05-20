package evm

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcap "github.com/smartcontractkit/chainlink-common/pkg/loop/chain-capabilities/evm"
	evmservicetypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/values/pb"
)

func (c *capability) CallContract(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.CallContractRequest) (*evmcap.CallContractReply, error) {
	callMsg, err := parseCallMsg(req.Call)
	if err != nil {
		return &evmcap.CallContractReply{}, err
	}

	blockNumber := &big.Int{}
	blockNumber, isOK := blockNumber.SetString(req.BlockNumber.String(), 10)
	if !isOK {
		return &evmcap.CallContractReply{}, fmt.Errorf("failed to parse block number: %s", req.BlockNumber.String())
	}

	response, err := c.EVMService.CallContract(ctx, &callMsg, blockNumber)
	if err != nil {
		return &evmcap.CallContractReply{}, err
	}

	return &evmcap.CallContractReply{Data: &evmcap.ABIPayload{Abi: response}}, nil
}

func (c *capability) FilterLogs(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.FilterLogsRequest) (*evmcap.FilterLogsReply, error) {
	evmCapFq := req.GetFilterQuery()

	fromBlock := &big.Int{}
	fromBlock, isOK := fromBlock.SetString(evmCapFq.FromBlock.String(), 10)
	if !isOK {
		return nil, fmt.Errorf("failed to parse from block number: %s", evmCapFq.FromBlock.String())
	}

	toBlock := &big.Int{}
	toBlock, isOK = toBlock.SetString(evmCapFq.ToBlock.String(), 10)
	if !isOK {
		return nil, fmt.Errorf("failed to parse to block number: %s", evmCapFq.ToBlock.String())
	}

	fq := evmservicetypes.FilterQuery{
		BlockHash: evmservicetypes.Hash(evmCapFq.BlockHash.Hash),
		FromBlock: fromBlock,
		ToBlock:   toBlock,
	}

	for _, address := range evmCapFq.Addresses {
		fq.Addresses = append(fq.Addresses, evmservicetypes.Address(address.Address))
	}

	// TODO fix topic type
	//for _, topic := range evmCapFq.Topics {
	//	fq.Addresses = append(fq.Addresses, evmservicetypes.Address(topic.Topic))
	//}

	logs, err := c.EVMService.FilterLogs(ctx, fq)
	if err != nil {
		return nil, err
	}

	return &evmcap.FilterLogsReply{Logs: parseLogs(logs)}, nil
}

func (c *capability) BalanceAt(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.BalanceAtRequest) (*evmcap.BalanceAtReply, error) {
	blockNumber := &big.Int{}
	blockNumber, isOK := blockNumber.SetString(req.BlockNumber.String(), 10)
	if !isOK {
		return nil, fmt.Errorf("failed to parse to block number: %s", req.BlockNumber.String())
	}

	balance, err := c.EVMService.BalanceAt(ctx, evmservicetypes.Address(req.Account.Address), blockNumber)
	if err != nil {
		return nil, err
	}

	return &evmcap.BalanceAtReply{Balance: &pb.BigInt{AbsVal: balance.Bytes(), Sign: int64(balance.Sign())}}, nil
}

func (c *capability) EstimateGas(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.EstimateGasRequest) (*evmcap.EstimateGasReply, error) {
	callMsg, err := parseCallMsg(req.Msg)
	if err != nil {
		return &evmcap.EstimateGasReply{}, err
	}

	estimate, err := c.EVMService.EstimateGas(ctx, &callMsg)
	if err != nil {
		return &evmcap.EstimateGasReply{}, err
	}

	return &evmcap.EstimateGasReply{Gas: estimate}, nil
}

func (c *capability) GetTransactionByHash(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.TransactionByHashRequest) (*evmcap.TransactionByHashReply, error) {
	tx, err := c.EVMService.TransactionByHash(ctx, evmservicetypes.Hash(req.Hash.Hash))
	if err != nil {
		return nil, err
	}

	return &evmcap.TransactionByHashReply{Transaction: &evmcap.Transaction{
		Nonce:    tx.Nonce,
		Gas:      tx.Gas,
		To:       &evmcap.Address{Address: tx.To[:]},
		Data:     &evmcap.ABIPayload{Abi: tx.Data},
		Value:    &pb.BigInt{AbsVal: tx.Value.Bytes(), Sign: int64(tx.Value.Sign())},
		GasPrice: &pb.BigInt{AbsVal: tx.GasPrice.Bytes(), Sign: int64(tx.GasPrice.Sign())},
		Hash:     &evmcap.Hash{Hash: tx.Hash[:]},
	}}, nil
}

func (c *capability) GetTransactionReceipt(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.TransactionReceiptRequest) (*evmcap.TransactionReceiptReply, error) {
	receipt, err := c.EVMService.TransactionReceipt(ctx, evmservicetypes.Hash(req.Hash.Hash))
	if err != nil {
		return nil, err
	}

	reply := &evmcap.TransactionReceiptReply{Receipt: &evmcap.Receipt{
		Status:          receipt.Status,
		Logs:            parseLogs(receipt.Logs),
		TxHash:          &evmcap.Hash{Hash: receipt.TxHash[:]},
		ContractAddress: &evmcap.Address{Address: receipt.ContractAddress[:]},
		GasUsed:         receipt.GasUsed,
		BlockHash:       &evmcap.Hash{Hash: receipt.BlockHash[:]},
		BlockNumber:     &pb.BigInt{AbsVal: receipt.BlockNumber.Bytes(), Sign: int64(receipt.BlockNumber.Sign())},
		// TODO add tx index
		//TransactionIndex:  tx.TransactionIndex,
		EffectiveGasPrice: &pb.BigInt{AbsVal: receipt.EffectiveGasPrice.Bytes(), Sign: int64(receipt.EffectiveGasPrice.Sign())},
	}}

	return reply, nil
}

func (c *capability) LatestAndFinalizedHead(ctx context.Context, _ capabilities.RequestMetadata, _ *emptypb.Empty) (*evmcap.LatestAndFinalizedHeadReply, error) {
	latest, finalized, err := c.EVMService.LatestAndFinalizedHead(ctx)
	if err != nil {
		return nil, err
	}

	return &evmcap.LatestAndFinalizedHeadReply{
		Latest: &evmcap.Head{
			Timestamp:   latest.Timestamp,
			BlockNumber: &pb.BigInt{AbsVal: latest.Number.Bytes(), Sign: int64(latest.Number.Sign())},
			Hash:        &evmcap.Hash{Hash: latest.Hash[:]},
			ParentHash:  &evmcap.Hash{Hash: latest.ParentHash[:]},
		},
		Finalized: &evmcap.Head{
			Timestamp:   finalized.Timestamp,
			BlockNumber: &pb.BigInt{AbsVal: finalized.Number.Bytes(), Sign: int64(finalized.Number.Sign())},
			Hash:        &evmcap.Hash{Hash: finalized.Hash[:]},
			ParentHash:  &evmcap.Hash{Hash: finalized.ParentHash[:]},
		},
	}, nil
}
func (c *capability) QueryTrackedLogs(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.QueryTrackedLogsRequest) (*evmcap.QueryTrackedLogsReply, error) {
	exp, err := evmcap.ConvertQueryTrackedLogsRequestFromProto(req)
	if err != nil {
		return nil, err
	}

	limitAndSort, err := evmcap.ConvertLimitAndSortFromProto(req.LimitAndSort)
	if err != nil {
		return nil, err
	}

	confidenceLevel, err := evmcap.ConfidenceFromProto(req.ConfidenceLevel)
	if err != nil {
		return nil, err
	}

	result, err := c.EVMService.QueryTrackedLogs(ctx, exp, limitAndSort, confidenceLevel)
	if err != nil {
		return nil, err
	}

	return &evmcap.QueryTrackedLogsReply{Logs: parseLogs(result)}, nil
}

func (c *capability) RegisterLogTracking(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.RegisterLogTrackingRequest) (*emptypb.Empty, error) {
	reqFilter := req.GetFilter()
	filter := evmservicetypes.LPFilterQuery{
		Name:         reqFilter.GetName(),
		Retention:    time.Duration(reqFilter.RetentionTime),
		MaxLogsKept:  reqFilter.MaxLogsKept,
		LogsPerBlock: reqFilter.LogsPerBlock,
	}

	for _, address := range reqFilter.Addresses {
		filter.Addresses = append(filter.Addresses, evmservicetypes.Address(address.GetAddress()))
	}

	for _, eventSig := range reqFilter.EventSigs {
		filter.EventSigs = append(filter.EventSigs, evmservicetypes.Hash(eventSig.GetHash()))
	}

	for _, topic := range reqFilter.Topic2 {
		filter.Topic2 = append(filter.Topic2, evmservicetypes.Hash(topic.GetHash()))
	}

	for _, topic := range reqFilter.Topic3 {
		filter.Topic2 = append(filter.Topic3, evmservicetypes.Hash(topic.GetHash()))
	}

	for _, topic := range reqFilter.Topic4 {
		filter.Topic4 = append(filter.Topic3, evmservicetypes.Hash(topic.GetHash()))
	}

	return &emptypb.Empty{}, c.EVMService.RegisterLogTracking(ctx, filter)
}

func (c *capability) UnregisterLogTracking(ctx context.Context, _ capabilities.RequestMetadata, req *evmcap.UnregisterLogTrackingRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, c.EVMService.UnregisterLogTracking(ctx, req.FilterName)
}

func parseCallMsg(callMsg *evmcap.CallMsg) (evmservicetypes.CallMsg, error) {
	parsed := evmservicetypes.CallMsg{
		To:   evmservicetypes.Address(callMsg.To.Address),
		From: evmservicetypes.Address(callMsg.From.Address),
		Data: callMsg.Data.Abi,
	}

	return parsed, nil
}

func parseLogs(logs []*evmservicetypes.Log) []*evmcap.Log {
	parsed := make([]*evmcap.Log, 0)
	for _, log := range logs {
		parsed = append(parsed, &evmcap.Log{
			Address:     &evmcap.Address{Address: log.Address[:]},
			Topics:      make([]*evmcap.Hash, 0),
			TxHash:      &evmcap.Hash{Hash: log.TxHash[:]},
			BlockHash:   &evmcap.Hash{Hash: log.BlockHash[:]},
			Data:        &evmcap.ABIPayload{Abi: log.Data},
			EventSig:    &evmcap.Hash{Hash: log.EventSig[:]},
			BlockNumber: &pb.BigInt{AbsVal: log.BlockNumber.Bytes(), Sign: int64(log.BlockNumber.Sign())},
			// TODO missing tx index from evm log
			//TxIndex: log.TxIndex,
			Index:   log.LogIndex,
			Removed: log.Removed,
		})
	}

	return parsed
}
