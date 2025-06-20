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

type request struct {
	node    string
	tsStart int64
	capabilities.CapabilityRequest
}

type MessageBuilder struct {
	ChainInfo chain_capabilities.ChainInfo
	CapInfo   capabilities.CapabilityInfo
}

// NewMessageBuilder creates a new message builder
func NewMessageBuilder(chainInfo chain_capabilities.ChainInfo, capInfo capabilities.CapabilityInfo) *MessageBuilder {
	return &MessageBuilder{
		ChainInfo: chainInfo,
		CapInfo:   capInfo,
	}
}

func (m *MessageBuilder) buildCallContractSuccess(request request, msg *evm.CallMsg, blockNumber *big.Int) *CallContractSuccess {
	return &CallContractSuccess{
		BlockNumber:     blockNumber.Int64(),
		ContractAddress: common.Bytes2Hex(msg.To[:]),
		// Execution Context - Source
		ExecutionContext: &commoncapbeholder.ExecutionContext{
			MetaSourceId: request.node,

			// Execution Context - Chain
			MetaChainFamilyName: m.ChainInfo.FamilyName,
			MetaChainId:         m.ChainInfo.ChainID,
			MetaNetworkName:     m.ChainInfo.NetworkName,
			MetaNetworkNameFull: m.ChainInfo.NetworkNameFull,

			// Execution Context - Workflow (capabilities.RequestMetadata)
			MetaWorkflowId:               request.Metadata.WorkflowID,
			MetaWorkflowOwner:            request.Metadata.WorkflowOwner,
			MetaWorkflowExecutionId:      request.Metadata.WorkflowExecutionID,
			MetaWorkflowName:             request.Metadata.WorkflowName,
			MetaWorkflowDonId:            request.Metadata.WorkflowDonID,
			MetaWorkflowDonConfigVersion: request.Metadata.WorkflowDonConfigVersion,
			MetaReferenceId:              request.Metadata.ReferenceID,

			// Execution Context - Capability
			MetaCapabilityType:           string(m.CapInfo.CapabilityType),
			MetaCapabilityId:             m.CapInfo.ID,
			MetaCapabilityTimestampStart: uint64(request.tsStart),
			MetaCapabilityTimestampEmit:  uint64(time.Now().UnixMilli()),
		},
	}
}

func (m *MessageBuilder) buildCallContractError(request request, msg *evm.CallMsg, blockNumber *big.Int, summary, cause string) *CallContractError {
	return &CallContractError{
		BlockNumber:     blockNumber.Int64(),
		ContractAddress: common.Bytes2Hex(msg.To[:]),
		Summary:         summary,
		Cause:           cause,
		// Execution Context - Source
		ExecutionContext: &commoncapbeholder.ExecutionContext{
			MetaSourceId: request.node,

			// Execution Context - Chain
			MetaChainFamilyName: m.ChainInfo.FamilyName,
			MetaChainId:         m.ChainInfo.ChainID,
			MetaNetworkName:     m.ChainInfo.NetworkName,
			MetaNetworkNameFull: m.ChainInfo.NetworkNameFull,

			// Execution Context - Workflow (capabilities.RequestMetadata)
			MetaWorkflowId:               request.Metadata.WorkflowID,
			MetaWorkflowOwner:            request.Metadata.WorkflowOwner,
			MetaWorkflowExecutionId:      request.Metadata.WorkflowExecutionID,
			MetaWorkflowName:             request.Metadata.WorkflowName,
			MetaWorkflowDonId:            request.Metadata.WorkflowDonID,
			MetaWorkflowDonConfigVersion: request.Metadata.WorkflowDonConfigVersion,
			MetaReferenceId:              request.Metadata.ReferenceID,

			// Execution Context - Capability
			MetaCapabilityType:           string(m.CapInfo.CapabilityType),
			MetaCapabilityId:             m.CapInfo.ID,
			MetaCapabilityTimestampStart: uint64(request.tsStart),
			MetaCapabilityTimestampEmit:  uint64(time.Now().UnixMilli()),
		},
	}
}
