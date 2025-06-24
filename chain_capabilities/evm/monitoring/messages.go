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

type ReadRequest struct {
	Node    string
	TsStart int64
	capabilities.RequestMetadata
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

func (m *MessageBuilder) BuildCallContractInitiated(request ReadRequest, msg *evm.CallMsg, blockNumber *big.Int) *CallContractInitiated {
	return &CallContractInitiated{
		Req: &CallContractRequest{
			BlockNumber:     blockNumber.Int64(),
			ContractAddress: common.Bytes2Hex(msg.To[:]),
		},
		ExecutionContext: m.BuildExecutionContext(request),
	}
}

func (m *MessageBuilder) BuildCallContractSuccess(request ReadRequest, msg *evm.CallMsg, blockNumber *big.Int) *CallContractSuccess {
	return &CallContractSuccess{
		Req: &CallContractRequest{
			BlockNumber:     blockNumber.Int64(),
			ContractAddress: common.Bytes2Hex(msg.To[:]),
		},
		ExecutionContext: m.BuildExecutionContext(request),
	}
}

func (m *MessageBuilder) BuildCallContractError(request ReadRequest, msg *evm.CallMsg, blockNumber *big.Int, summary, cause string) *CallContractError {
	return &CallContractError{
		Req: &CallContractRequest{
			BlockNumber:     blockNumber.Int64(),
			ContractAddress: common.Bytes2Hex(msg.To[:]),
		},
		Summary:          summary,
		Cause:            cause,
		ExecutionContext: m.BuildExecutionContext(request),
	}
}

func (m *MessageBuilder) BuildExecutionContext(request ReadRequest) *commoncapbeholder.ExecutionContext {
	ex := &commoncapbeholder.ExecutionContext{
		MetaSourceId: request.Node,

		// Execution Context - Chain
		MetaChainFamilyName: m.ChainInfo.FamilyName,
		MetaChainId:         m.ChainInfo.ChainID,
		MetaNetworkName:     m.ChainInfo.NetworkName,
		MetaNetworkNameFull: m.ChainInfo.NetworkNameFull,

		// Execution Context - Workflow (capabilities.RequestMetadata)
		MetaWorkflowId:               request.WorkflowID,
		MetaWorkflowOwner:            request.WorkflowOwner,
		MetaWorkflowExecutionId:      request.WorkflowExecutionID,
		MetaWorkflowName:             request.WorkflowName,
		MetaWorkflowDonId:            request.WorkflowDonID,
		MetaWorkflowDonConfigVersion: request.WorkflowDonConfigVersion,
		MetaReferenceId:              request.ReferenceID,

		// Execution Context - Capability
		MetaCapabilityType:           string(m.CapInfo.CapabilityType),
		MetaCapabilityId:             m.CapInfo.ID,
		MetaCapabilityTimestampStart: uint64(request.TsStart),
		MetaCapabilityTimestampEmit:  uint64(time.Now().UnixMilli()),
	}
	return ex
}
