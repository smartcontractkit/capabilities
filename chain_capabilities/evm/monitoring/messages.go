package monitoring

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
)

// ReadRequest wraps context for telemetry
type ReadRequest struct {
	TsStart int64
	capabilities.RequestMetadata
}

// MessageBuilder constructs telemetry messages for EVM calls
type MessageBuilder struct {
	ChainInfo   types.ChainInfo
	CapInfo     capabilities.CapabilityInfo
	nodeAddress string
}

type Message interface {
	proto.Message
	Attributes() []attribute.KeyValue
}

type ErrorMessage interface {
	Message
	GetSummary() string
	GetCause() string
}

// NewMessageBuilder creates a new builder
func NewMessageBuilder(chainInfo types.ChainInfo, capInfo capabilities.CapabilityInfo, nodeAddress string) *MessageBuilder {
	return &MessageBuilder{ChainInfo: chainInfo, CapInfo: capInfo, nodeAddress: nodeAddress}
}

func (m *MessageBuilder) BuildCallContractInitiated(r ReadRequest, msg *evm.CallMsg, bn *big.Int) *CallContractInitiated {
	return &CallContractInitiated{Req: &CallContractRequest{BlockNumber: bn.Int64(), ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildCallContractSuccess(r ReadRequest, msg *evm.CallMsg, bn *big.Int) Message {
	return &CallContractSuccess{Req: &CallContractRequest{BlockNumber: bn.Int64(), ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildCallContractError(r ReadRequest, msg *evm.CallMsg, bn *big.Int, summary, cause string) ErrorMessage {
	return &CallContractError{Req: &CallContractRequest{BlockNumber: bn.Int64(), ContractAddress: common.Bytes2Hex(msg.To[:])}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildFilterLogsInitiated(r ReadRequest, fq evmtypes.FilterQuery) *FilterLogsInitiated {
	return &FilterLogsInitiated{Req: toFilterLogsRequest(fq), ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildFilterLogsSuccess(r ReadRequest, fq evmtypes.FilterQuery, count int32) Message {
	return &FilterLogsSuccess{Req: toFilterLogsRequest(fq), LogCount: count, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildFilterLogsError(r ReadRequest, fq evmtypes.FilterQuery, summary, cause string) ErrorMessage {
	return &FilterLogsError{Req: toFilterLogsRequest(fq), Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtInitiated(r ReadRequest, account string, bn *big.Int) *BalanceAtInitiated {
	return &BalanceAtInitiated{Req: &BalanceAtRequest{Account: account, BlockNumber: bn.Int64()}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtSuccess(r ReadRequest, account string, bn, bal *big.Int) Message {
	return &BalanceAtSuccess{Req: &BalanceAtRequest{Account: account, BlockNumber: bn.Int64()}, Balance: bal.String(), ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtError(r ReadRequest, account string, bn *big.Int, summary, cause string) ErrorMessage {
	return &BalanceAtError{Req: &BalanceAtRequest{Account: account, BlockNumber: bn.Int64()}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildEstimateGasInitiated(r ReadRequest, from, to string, data []byte) *EstimateGasInitiated {
	return &EstimateGasInitiated{Req: &EstimateGasRequest{From: from, To: to, Data: data}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildEstimateGasSuccess(r ReadRequest, from, to string, data []byte, gas int64) Message {
	return &EstimateGasSuccess{Req: &EstimateGasRequest{From: from, To: to, Data: data}, Gas: gas, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildEstimateGasError(r ReadRequest, from, to string, data []byte, summary, cause string) ErrorMessage {
	return &EstimateGasError{Req: &EstimateGasRequest{From: from, To: to, Data: data}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionByHashInitiated(r ReadRequest, hash string) *GetTransactionByHashInitiated {
	return &GetTransactionByHashInitiated{Req: &GetTransactionByHashRequest{Hash: hash}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionByHashSuccess(r ReadRequest, hash string, tx *evm.Transaction) Message {
	return &GetTransactionByHashSuccess{Req: &GetTransactionByHashRequest{Hash: hash}, Transaction: &TransactionData{
		TxHash:   common.Bytes2Hex(tx.Hash[:]),
		TxNonce:  tx.Nonce,
		Gas:      tx.Gas,
		GasPrice: tx.GasPrice.Uint64(),
		Value:    tx.Value.Uint64(),
	}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionByHashError(r ReadRequest, hash, summary, cause string) ErrorMessage {
	return &GetTransactionByHashError{Req: &GetTransactionByHashRequest{Hash: hash}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptInitiated(r ReadRequest, hash string) *GetTransactionReceiptInitiated {
	return &GetTransactionReceiptInitiated{Req: &GetTransactionReceiptRequest{Hash: hash}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptSuccess(r ReadRequest, hash string, receipt *evm.Receipt) Message {
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

func (m *MessageBuilder) BuildGetTransactionReceiptError(r ReadRequest, hash, summary, cause string) ErrorMessage {
	return &GetTransactionReceiptError{Req: &GetTransactionReceiptRequest{Hash: hash}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildLatestAndFinalizedHeadInitiated(r ReadRequest) *LatestAndFinalizedHeadInitiated {
	return &LatestAndFinalizedHeadInitiated{ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildLatestAndFinalizedHeadSuccess(r ReadRequest, latest, finalized evm.Head) Message {
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

func (m *MessageBuilder) BuildLatestAndFinalizedHeadError(r ReadRequest, summary, cause string) ErrorMessage {
	return &LatestAndFinalizedHeadError{Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

// BuildExecutionContext builds the shared ExecutionContext
func (m *MessageBuilder) BuildExecutionContext(request ReadRequest) *capmonitoring.ExecutionContext {
	ex := &capmonitoring.ExecutionContext{
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

func toFilterLogsRequest(fq evmtypes.FilterQuery) *FilterLogsRequest {
	hexAddresses := make([]string, 0, len(fq.Addresses))
	for _, addr := range fq.Addresses {
		hexAddresses = append(hexAddresses, common.BytesToAddress(addr[:]).Hex())
	}

	hexTopics := make([]*Topics, 0, len(fq.Topics))
	for _, topicList := range fq.Topics {
		var hexTopicsList []string
		for _, topic := range topicList {
			hexTopicsList = append(hexTopicsList, common.Bytes2Hex(topic[:]))
		}
		hexTopics = append(hexTopics, &Topics{Topic: hexTopicsList})
	}

	return &FilterLogsRequest{
		FromBlock: fq.FromBlock.Int64(),
		ToBlock:   fq.ToBlock.Int64(),
		BlockHash: common.Bytes2Hex(fq.BlockHash[:]),
		Addresses: hexAddresses,
		Topics:    hexTopics,
	}
}
