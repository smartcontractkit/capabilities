package readcontract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	types2 "github.com/smartcontractkit/chainlink-common/pkg/types"
	types "github.com/smartcontractkit/chainlink-evm/pkg/config"
	"github.com/smartcontractkit/chainlink/v2/core/services/job"
	"github.com/smartcontractkit/chainlink/v2/core/testdata/testspecs"
)

var (
	workflowName    = "abcdef0123"
	workflowOwnerID = "0100000000000000000000000000000000000001"
)

const workflowTemplateReadContract = `
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

targets:
  - id: "mock-target@1.0.0"
    ref: "target"
    inputs:
      $(action2.outputs)
    config:
      mustputavaluehere_thisisabug: "true"
`

func CreateWorkflowJobForTest(
	t *testing.T,
	workflowName string,
	workflowOwner string,
	networkID string,
	chainID string,
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
		workflowTemplateReadContract,
		workflowName,
		workflowOwner,
		networkID,
		chainID,
		contractAddress,
		contractName,
		readIdentifier,
		contractReaderConfig,
	)

	fmt.Println("Test Job Spec->")
	fmt.Println(jobSpecString)

	workflowJobSpec := testspecs.GenerateWorkflowJobSpec(
		t,
		jobSpecString,
	)

	return workflowJobSpec.Job()
}

func CreateContractReaderConfig(contractName string, contractFuncName string, contractABI string) (string, error) {
	marshalledABIJson, err := json.Marshal(contractABI)
	if err != nil {
		return "", fmt.Errorf("failed to marshal contract ABI: %v", err)
	}

	marshalledABIJson = marshalledABIJson[1 : len(marshalledABIJson)-1]

	contractReaderConfig := types.ChainReaderConfig{
		Contracts: map[string]types.ChainContractReader{
			contractName: {
				ContractABI: string(marshalledABIJson),
				Configs: map[string]*types.ChainReaderDefinition{
					contractFuncName: {
						ChainSpecificName: contractFuncName,
					},
				},
			},
		},
	}

	contractReaderConfigEncoded, err := json.Marshal(contractReaderConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal contract reader config: %v", err)
	}

	contractReadConfig := strings.ReplaceAll(string(contractReaderConfigEncoded), "\\n", "")
	contractReadConfig = strings.ReplaceAll(contractReadConfig, "\\\"chainSpecificName\\\"", "\\\\\\\"chainSpecificName\\\\\\\"")
	contractReadConfig = strings.ReplaceAll(contractReadConfig, "\\\""+contractFuncName+"\\\"", "\\\\\\\""+contractFuncName+"\\\\\\\"")
	return contractReadConfig, nil
}
