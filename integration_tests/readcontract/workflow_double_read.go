package readcontract

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	types2 "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink/v2/core/services/job"
	"github.com/smartcontractkit/chainlink/v2/core/testdata/testspecs"
)

const workflowTemplateDoubleReadContract = `
name: "%s"
owner: "0x%s"
triggers:
  - id: "mock-trigger@1.0.0"
    ref: "trigger"
    config:
       mustputavaluehere_thisisabug: "true"

actions:
  - id: "read-contract-%s-%s@1.0.0"
    ref: "action2"
    inputs:
      $(trigger.outputs)
    config:
      ContractAddress: "%s"
      ContractName: "%s"
      ReadIdentifier: "%s"
      ContractReaderConfig: |
        %s
  - id: "read-contract-%s-%s@1.0.0"
    ref: "action3"
    inputs:
      $(trigger.outputs)
    config:
      ContractAddress: "%s"
      ContractName: "%s"
      ReadIdentifier: "%s"
      ContractReaderConfig: |
        %s

targets:
  - id: "mock-target@1.0.0"
    ref: "target"
    inputs:
      $(action3.outputs)
    config:
      mustputavaluehere_thisisabug: "true"
`

func CreateDoubleReadWorkflowJobForTest(
	t *testing.T,
	workflowName string,
	workflowOwner string,
	networkID string,
	chainID string,
	firstReaderAddress string,
	contractAddress string,
	contractName string,
	contractFuncName string,
	contractABI string,
) job.Job {
	contractReaderConfig, err := CreateContractReaderConfig(contractName, contractFuncName, contractABI)
	require.NoError(t, err)

	valueSourceContract := types2.BoundContract{
		Address: contractAddress,
		Name:    contractName,
	}

	readIdentifier := valueSourceContract.ReadIdentifier(contractFuncName)

	jobSpecString := fmt.Sprintf(
		workflowTemplateDoubleReadContract,
		workflowName,
		workflowOwner,
		networkID,
		chainID,
		firstReaderAddress,
		contractName,
		readIdentifier,
		contractReaderConfig,
		networkID,
		chainID,
		contractAddress,
		contractName,
		readIdentifier,
		contractReaderConfig,
	)

	fmt.Println("Double Read Test Job Spec->")
	fmt.Println(jobSpecString)

	workflowJobSpec := testspecs.GenerateWorkflowJobSpec(
		t,
		jobSpecString,
	)

	return workflowJobSpec.Job()
}
