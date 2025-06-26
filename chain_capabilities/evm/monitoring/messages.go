package monitoring

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/capabilities/chain_capabilities"
	commoncapbeholder "github.com/smartcontractkit/capabilities/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
)

// ReadRequest wraps context for telemetry
type ReadRequest struct {
	TsStart int64
	capabilities.RequestMetadata
}

// MessageBuilder constructs telemetry messages for EVM calls
type MessageBuilder struct {
	ChainInfo   chain_capabilities.ChainInfo
	CapInfo     capabilities.CapabilityInfo
	nodeAddress string
}

// NewMessageBuilder creates a new builder
func NewMessageBuilder(chainInfo chain_capabilities.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{ChainInfo: chainInfo, CapInfo: capInfo, nodeAddress: nodeAddress}
}

func (m *MessageBuilder) BuildCallContractInitiated(r ReadRequest, msg *evm.CallMsg, bn *big.Int) *CallContractInitiated {
	return &CallContractInitiated{Req: &CallContractRequest{BlockNumber: bn.Int64(), ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildCallContractSuccess(r ReadRequest, msg *evm.CallMsg, bn *big.Int) *CallContractSuccess {
	return &CallContractSuccess{Req: &CallContractRequest{BlockNumber: bn.Int64(), ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildCallContractError(r ReadRequest, msg *evm.CallMsg, bn *big.Int, summary, cause string) *CallContractError {
	return &CallContractError{Req: &CallContractRequest{BlockNumber: bn.Int64(), ContractAddress: common.Bytes2Hex(msg.To[:])}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildFilterLogsInitiated(r ReadRequest, from, to *big.Int) *FilterLogsInitiated {
	return &FilterLogsInitiated{Req: &FilterLogsRequest{FromBlock: from.Int64(), ToBlock: to.Int64()}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildFilterLogsSuccess(r ReadRequest, from, to *big.Int, count int32) *FilterLogsSuccess {
	return &FilterLogsSuccess{Req: &FilterLogsRequest{FromBlock: from.Int64(), ToBlock: to.Int64()}, LogCount: count, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildFilterLogsError(r ReadRequest, from, to *big.Int, summary, cause string) *FilterLogsError {
	return &FilterLogsError{Req: &FilterLogsRequest{FromBlock: from.Int64(), ToBlock: to.Int64()}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtInitiated(r ReadRequest, account string, bn *big.Int) *BalanceAtInitiated {
	return &BalanceAtInitiated{Req: &BalanceAtRequest{Account: account, BlockNumber: bn.Int64()}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtSuccess(r ReadRequest, account string, bn, bal *big.Int) *BalanceAtSuccess {
	return &BalanceAtSuccess{Req: &BalanceAtRequest{Account: account, BlockNumber: bn.Int64()}, Balance: bal.String(), ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtError(r ReadRequest, account string, bn *big.Int, summary, cause string) *BalanceAtError {
	return &BalanceAtError{Req: &BalanceAtRequest{Account: account, BlockNumber: bn.Int64()}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildEstimateGasInitiated(r ReadRequest, from, to string, data []byte) *EstimateGasInitiated {
	return &EstimateGasInitiated{Req: &EstimateGasRequest{From: from, To: to, Data: data}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildEstimateGasSuccess(r ReadRequest, from, to string, data []byte, gas int64) *EstimateGasSuccess {
	return &EstimateGasSuccess{Req: &EstimateGasRequest{From: from, To: to, Data: data}, Gas: gas, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildEstimateGasError(r ReadRequest, from, to string, data []byte, summary, cause string) *EstimateGasError {
	return &EstimateGasError{Req: &EstimateGasRequest{From: from, To: to, Data: data}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionByHashInitiated(r ReadRequest, hash string) *GetTransactionByHashInitiated {
	return &GetTransactionByHashInitiated{Req: &GetTransactionByHashRequest{Hash: hash}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionByHashSuccess(r ReadRequest, hash string, tx *evm.Transaction) *GetTransactionByHashSuccess {
	return &GetTransactionByHashSuccess{Req: &GetTransactionByHashRequest{Hash: hash}, Transaction: &TransactionData{
		TxHash:   common.Bytes2Hex(tx.Hash[:]),
		TxNonce:  tx.Nonce,
		Gas:      tx.Gas,
		GasPrice: tx.GasPrice.Uint64(),
		Value:    tx.Value.Uint64(),
	}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionByHashError(r ReadRequest, hash, summary, cause string) *GetTransactionByHashError {
	return &GetTransactionByHashError{Req: &GetTransactionByHashRequest{Hash: hash}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptInitiated(r ReadRequest, hash string) *GetTransactionReceiptInitiated {
	return &GetTransactionReceiptInitiated{Req: &GetTransactionReceiptRequest{Hash: hash}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptSuccess(r ReadRequest, hash string, receipt *evm.Receipt) *GetTransactionReceiptSuccess {
	return &GetTransactionReceiptSuccess{Req: &GetTransactionReceiptRequest{Hash: hash}, Receipt: &Receipt{
		Status:            receipt.Status,
		TxHash:            common.BytesToHash(receipt.TxHash[:]).String(),
		ContractAddress:   common.BytesToAddress(receipt.ContractAddress[:]).String(),
		GasUsed:           receipt.GasUsed,
		BlockHash:         common.BytesToHash(receipt.BlockHash[:]).String(),
		BlockNumber:       receipt.BlockNumber.Uint64(),
		TransactionIndex:  receipt.TransactionIndex,
		EffectiveGasPrice: receipt.EffectiveGasPrice.Uint64(),
	}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptError(r ReadRequest, hash, summary, cause string) *GetTransactionReceiptError {
	return &GetTransactionReceiptError{Req: &GetTransactionReceiptRequest{Hash: hash}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildLatestAndFinalizedHeadInitiated(r ReadRequest) *LatestAndFinalizedHeadInitiated {
	return &LatestAndFinalizedHeadInitiated{ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildLatestAndFinalizedHeadSuccess(r ReadRequest, latest, finalized evm.Head) *LatestAndFinalizedHeadSuccess {
	return &LatestAndFinalizedHeadSuccess{Latest: &BlockData{
		BlockHash:      common.Bytes2Hex(latest.Hash[:]),
		BlockHeight:    latest.Number.String(),
		BlockTimestamp: latest.Timestamp,
	},
		Finalized: &BlockData{
			BlockHash:      common.Bytes2Hex(finalized.Hash[:]),
			BlockHeight:    finalized.Number.String(),
			BlockTimestamp: finalized.Timestamp,
		},
		ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildLatestAndFinalizedHeadError(r ReadRequest, summary, cause string) *LatestAndFinalizedHeadError {
	return &LatestAndFinalizedHeadError{Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

// BuildExecutionContext builds the shared ExecutionContext
func (m *MessageBuilder) BuildExecutionContext(request ReadRequest) *commoncapbeholder.ExecutionContext {
	ex := &commoncapbeholder.ExecutionContext{
		MetaSourceId: m.nodeAddress,

		// Chain
		MetaChainFamilyName: m.ChainInfo.FamilyName,
		MetaChainId:         m.ChainInfo.ChainID,
		MetaNetworkName:     m.ChainInfo.NetworkName,
		MetaNetworkNameFull: m.ChainInfo.NetworkNameFull,

		// Workflow
		MetaWorkflowId:               request.WorkflowID,
		MetaWorkflowOwner:            request.WorkflowOwner,
		MetaWorkflowExecutionId:      request.WorkflowExecutionID,
		MetaWorkflowName:             request.WorkflowName,
		MetaWorkflowDonId:            request.WorkflowDonID,
		MetaWorkflowDonConfigVersion: request.WorkflowDonConfigVersion,
		MetaReferenceId:              request.ReferenceID,
		// Capability
		MetaCapabilityType: string(m.CapInfo.CapabilityType),
		MetaCapabilityId:   m.CapInfo.ID,
		// G115: integer overflow conversion uint64 -> int64 (gosec)
		// nolint:gosec
		MetaCapabilityTimestampStart: uint64(request.TsStart),
		// G115: integer overflow conversion uint64 -> int64 (gosec)
		// nolint:gosec
		MetaCapabilityTimestampEmit: uint64(time.Now().UnixMilli()),
	}
	return ex
}
