package monitoring

import (
	"fmt"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/proto"

	evmcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	sdkpb "github.com/smartcontractkit/chainlink-protos/cre/go/sdk"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"

	capmonitoring "github.com/smartcontractkit/capabilities/libs/monitoring"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
	evmtypes "github.com/smartcontractkit/chainlink-common/pkg/types/chains/evm"
)

// TelemetryContext wraps context for telemetry
type TelemetryContext struct {
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

func (m *MessageBuilder) BuildCallContractInitiated(tc TelemetryContext, msg *evm.CallMsg, bn int64) *CallContractInitiated {
	return &CallContractInitiated{Req: &CallContractRequest{BlockNumber: bn, ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildCallContractSuccess(tc TelemetryContext, msg *evm.CallMsg, bn int64) Message {
	return &CallContractSuccess{Req: &CallContractRequest{BlockNumber: bn, ContractAddress: common.Bytes2Hex(msg.To[:])}, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildCallContractError(tc TelemetryContext, msg *evm.CallMsg, bn int64, summary, cause string) ErrorMessage {
	return &CallContractError{Req: &CallContractRequest{BlockNumber: bn, ContractAddress: common.Bytes2Hex(msg.To[:])}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildWriteReportInitiated(tc TelemetryContext, req *evmcap.WriteReportRequest) *WriteReportInitiated {
	return &WriteReportInitiated{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc)}
}

func convertWriteReportRequest(req *evmcap.WriteReportRequest) *WriteReportRequest {
	return &WriteReportRequest{
		Receiver: req.Receiver,
		Report: &ReportResponse{
			ConfigDigest:  req.Report.ConfigDigest,
			SeqNr:         req.Report.SeqNr,
			ReportContext: req.Report.ReportContext,
			RawReport:     req.Report.RawReport,
			Sigs:          convertAttributedSignature(req.Report.Sigs),
		},
	}
}

func convertAttributedSignature(attributedSignatures []*sdkpb.AttributedSignature) []*AttributedSignature {
	convertedSignatures := []*AttributedSignature{}
	for _, as := range attributedSignatures {
		convertedSignatures = append(convertedSignatures, &AttributedSignature{
			Signature: as.Signature,
			SignerId:  as.SignerId,
		})
	}
	return convertedSignatures
}

func (m *MessageBuilder) BuildWriteReportSuccess(tc TelemetryContext, req *evmcap.WriteReportRequest) *WriteReportSuccess {
	return &WriteReportSuccess{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildWriteReportError(tc TelemetryContext, req *evmcap.WriteReportRequest, summary, cause string) ErrorMessage {
	return &WriteReportError{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
	}
}

func (m *MessageBuilder) BuildWriteReportTxFeeCalculationError(tc TelemetryContext, req *evmcap.WriteReportRequest, txIdempotencyKey, cause string) ErrorMessage {
	summary := "Failed to calculate transaction fee"
	if txIdempotencyKey != "" {
		summary = fmt.Sprintf("Failed to calculate transaction fee for tx: %s", txIdempotencyKey)
	}
	return &WriteReportTxFeeCalculationError{
		Req:              convertWriteReportRequest(req),
		ExecutionContext: m.BuildExecutionContext(tc),
		Summary:          summary,
		Cause:            cause,
		TxIdempotencyKey: txIdempotencyKey,
	}
}

func (m *MessageBuilder) BuildLogTriggerInitiated(tc TelemetryContext, req *evmcap.FilterLogTriggerRequest) *TriggerInitiated {
	return &TriggerInitiated{Req: req, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildLogTriggerSuccess(tc TelemetryContext, triggerID string, req *evmcap.FilterLogTriggerRequest, logCount int, latestOffsetBlock int64) Message {
	return &LogTriggerSuccess{
		TriggerID:         triggerID,
		Req:               req,
		LogCount:          int32(logCount), // nolint:gosec // G115: integer overflow conversion int -> int32 (gosec)
		LatestOffsetBlock: latestOffsetBlock,
		ExecutionContext:  m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildLogTriggerError(tc TelemetryContext, triggerID string, summary, cause string) ErrorMessage {
	return &LogTriggerError{
		TriggerID:        triggerID,
		Summary:          summary,
		Cause:            cause,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildLogTriggerCleanUpError(tc TelemetryContext, summary, cause string) ErrorMessage {
	return &LogTriggerCleanUpError{
		Summary:          summary,
		Cause:            cause,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildLogTriggerEventDroppedError(tc TelemetryContext, triggerID string, log *evm.Log, summary, cause string) ErrorMessage {
	return &LogTriggerEventDroppedError{
		TriggerID:        triggerID,
		TxHash:           common.Bytes2Hex(log.TxHash[:]),
		BlockHash:        common.Bytes2Hex(log.BlockHash[:]),
		LogIndex:         int64(log.LogIndex),
		Summary:          summary,
		Cause:            cause,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildFilterLogsInitiated(tc TelemetryContext, fq evmtypes.FilterQuery) *FilterLogsInitiated {
	return &FilterLogsInitiated{Req: toFilterLogsRequest(fq), ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildFilterLogsSuccess(tc TelemetryContext, fq evmtypes.FilterQuery, count int32) Message {
	return &FilterLogsSuccess{Req: toFilterLogsRequest(fq), LogCount: count, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildFilterLogsError(tc TelemetryContext, fq evmtypes.FilterQuery, summary, cause string) ErrorMessage {
	return &FilterLogsError{Req: toFilterLogsRequest(fq), Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildBalanceAtInitiated(tc TelemetryContext, account string, bn int64) *BalanceAtInitiated {
	return &BalanceAtInitiated{Req: &BalanceAtRequest{Account: account, BlockNumber: bn}, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildBalanceAtSuccess(tc TelemetryContext, account string, bn int64, bal *big.Int) Message {
	return &BalanceAtSuccess{Req: &BalanceAtRequest{Account: account, BlockNumber: bn}, Balance: bal.String(), ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildBalanceAtError(tc TelemetryContext, account string, bn int64, summary, cause string) ErrorMessage {
	return &BalanceAtError{Req: &BalanceAtRequest{Account: account, BlockNumber: bn}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildEstimateGasInitiated(tc TelemetryContext, from, to string, data []byte) *EstimateGasInitiated {
	return &EstimateGasInitiated{Req: &EstimateGasRequest{From: from, To: to, Data: data}, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildEstimateGasSuccess(tc TelemetryContext, from, to string, data []byte, gas int64) Message {
	return &EstimateGasSuccess{Req: &EstimateGasRequest{From: from, To: to, Data: data}, Gas: gas, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildEstimateGasError(tc TelemetryContext, from, to string, data []byte, summary, cause string) ErrorMessage {
	return &EstimateGasError{Req: &EstimateGasRequest{From: from, To: to, Data: data}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildGetTransactionByHashInitiated(tc TelemetryContext, hash string) *GetTransactionByHashInitiated {
	return &GetTransactionByHashInitiated{Req: &GetTransactionByHashRequest{Hash: hash}, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildGetTransactionByHashSuccess(tc TelemetryContext, hash string, tx *evmcap.Transaction) Message {
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
	return &GetTransactionByHashSuccess{Req: &GetTransactionByHashRequest{Hash: hash}, Transaction: txData, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildGetTransactionByHashError(tc TelemetryContext, hash, summary, cause string) ErrorMessage {
	return &GetTransactionByHashError{Req: &GetTransactionByHashRequest{Hash: hash}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptInitiated(tc TelemetryContext, hash string) *GetTransactionReceiptInitiated {
	return &GetTransactionReceiptInitiated{Req: &GetTransactionReceiptRequest{Hash: hash}, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptSuccess(tc TelemetryContext, hash string, receipt *evmcap.Receipt) Message {
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

	return &GetTransactionReceiptSuccess{Req: &GetTransactionReceiptRequest{Hash: hash}, Receipt: receiptData, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildGetTransactionReceiptError(tc TelemetryContext, hash, summary, cause string) ErrorMessage {
	return &GetTransactionReceiptError{Req: &GetTransactionReceiptRequest{Hash: hash}, Summary: summary, Cause: cause, ExecutionContext: m.BuildExecutionContext(tc)}
}

func (m *MessageBuilder) BuildHeaderByNumberInitiated(tc TelemetryContext, blockNumber int64) *HeaderByNumberInitiated {
	return &HeaderByNumberInitiated{ExecutionContext: m.BuildExecutionContext(tc), Req: &HeaderByNumberRequest{BlockNumber: blockNumber}}
}

func (m *MessageBuilder) BuildHeaderByNumberSuccess(tc TelemetryContext, blockNumber int64, header *evmcap.Header) Message {
	return &HeaderByNumberSuccess{
		Req: &HeaderByNumberRequest{BlockNumber: blockNumber},
		Header: &BlockData{
			BlockHash:      common.Bytes2Hex(header.Hash[:]),
			BlockHeight:    header.BlockNumber.String(),
			BlockTimestamp: header.Timestamp,
		},
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

func (m *MessageBuilder) BuildHeaderByNumberError(tc TelemetryContext, blockNumber int64, summary, cause string) ErrorMessage {
	return &HeaderByNumberError{
		Req:              &HeaderByNumberRequest{BlockNumber: blockNumber},
		Summary:          summary,
		Cause:            cause,
		ExecutionContext: m.BuildExecutionContext(tc),
	}
}

// BuildExecutionContext builds the shared ExecutionContext
func (m *MessageBuilder) BuildExecutionContext(tc TelemetryContext) *capmonitoring.ExecutionContext {
	ex := &capmonitoring.ExecutionContext{
		MetaSourceId: m.nodeAddress,

		// Chain
		MetaChainFamilyName: m.ChainInfo.FamilyName,
		MetaChainId:         m.ChainInfo.ChainID,
		MetaNetworkName:     m.ChainInfo.NetworkName,
		MetaNetworkNameFull: m.ChainInfo.NetworkNameFull,

		// Workflow
		MetaWorkflowId:               tc.WorkflowID,
		MetaWorkflowOwner:            tc.WorkflowOwner,
		MetaWorkflowExecutionId:      tc.WorkflowExecutionID,
		MetaWorkflowName:             tc.WorkflowName,
		MetaWorkflowDonId:            tc.WorkflowDonID,
		MetaWorkflowDonConfigVersion: tc.WorkflowDonConfigVersion,
		MetaReferenceId:              tc.ReferenceID,
		// Capability
		MetaCapabilityType: string(m.CapInfo.CapabilityType),
		MetaCapabilityId:   m.CapInfo.ID,
		// G115: integer overflow conversion uint64 -> int64 (gosec)
		// nolint:gosec
		MetaCapabilityTimestampStart: uint64(tc.TsStart),
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
