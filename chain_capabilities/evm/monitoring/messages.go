package monitoring

import (
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	valuespb "github.com/smartcontractkit/chainlink-common/pkg/values/pb"
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

func (m *MessageBuilder) BuildCallContractInitiated(r ReadRequest, msg *evm.CallMsg, bn int64) *CallContractInitiated {
	return &CallContractInitiated{Req: &CallContractRequest{BlockNumber: bn, ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildCallContractSuccess(r ReadRequest, msg *evm.CallMsg, bn int64) Message {
	return &CallContractSuccess{Req: &CallContractRequest{BlockNumber: bn, ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildCallContractError(r ReadRequest, msg *evm.CallMsg, bn int64, summary, cause string) ErrorMessage {
	return &CallContractError{Req: &CallContractRequest{BlockNumber: bn, ContractAddress: common.Bytes2Hex(msg.To[:])}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
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

func (m *MessageBuilder) BuildBalanceAtInitiated(r ReadRequest, account string, bn int64) *BalanceAtInitiated {
	return &BalanceAtInitiated{Req: &BalanceAtRequest{Account: account, BlockNumber: bn}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtSuccess(r ReadRequest, account string, bn int64, bal *big.Int) Message {
	return &BalanceAtSuccess{Req: &BalanceAtRequest{Account: account, BlockNumber: bn}, Balance: bal.String(), ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildBalanceAtError(r ReadRequest, account string, bn int64, summary, cause string) ErrorMessage {
	return &BalanceAtError{Req: &BalanceAtRequest{Account: account, BlockNumber: bn}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
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

func (m *MessageBuilder) BuildGetTransactionByHashSuccess(r ReadRequest, hash string, tx *evmcap.Transaction) Message {
	txData := &TransactionData{
		TxHash:  common.Bytes2Hex(tx.Hash[:]),
		TxNonce: tx.Nonce,
		Gas:     tx.Gas,
	}
	if tx.GasPrice != nil {
		txData.GasPrice = valuespb.NewIntFromBigInt(tx.GasPrice).Uint64()
	}
	if tx.Value != nil {
		txData.Value = valuespb.NewIntFromBigInt(tx.Value).Uint64()
	}
	return &GetTransactionByHashSuccess{Req: &GetTransactionByHashRequest{Hash: hash}, Transaction: txData, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionByHashError(r ReadRequest, hash, summary, cause string) ErrorMessage {
	return &GetTransactionByHashError{Req: &GetTransactionByHashRequest{Hash: hash}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptInitiated(r ReadRequest, hash string) *GetTransactionReceiptInitiated {
	return &GetTransactionReceiptInitiated{Req: &GetTransactionReceiptRequest{Hash: hash}, ExecutionContext: m.BuildExecutionContext(r)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptSuccess(r ReadRequest, hash string, receipt *evmcap.Receipt) Message {
	receiptData := &Receipt{
		Status:           receipt.Status,
		TxHash:           common.BytesToHash(receipt.TxHash[:]).String(),
		ContractAddress:  common.BytesToAddress(receipt.ContractAddress[:]).String(),
		GasUsed:          receipt.GasUsed,
		BlockHash:        common.BytesToHash(receipt.BlockHash[:]).String(),
		TransactionIndex: receipt.TxIndex,
	}

	if receipt.BlockNumber != nil {
		receiptData.BlockNumber = valuespb.NewIntFromBigInt(receipt.BlockNumber).Uint64()
	}

	if receipt.EffectiveGasPrice != nil {
		receiptData.EffectiveGasPrice = valuespb.NewIntFromBigInt(receipt.EffectiveGasPrice).Uint64()
	}

	return &GetTransactionReceiptSuccess{Req: &GetTransactionReceiptRequest{Hash: hash}, Receipt: receiptData, ExecutionContext: m.BuildExecutionContext(r)}
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

	result := &FilterLogsRequest{
		BlockHash: common.Bytes2Hex(fq.BlockHash[:]),
		Addresses: hexAddresses,
		Topics:    hexTopics,
	}

	if fq.FromBlock != nil {
		result.FromBlock = fq.FromBlock.Int64()
	}

	if fq.ToBlock != nil {
		result.ToBlock = fq.ToBlock.Int64()
	}

	return result
}
