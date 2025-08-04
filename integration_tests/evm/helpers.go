package evm

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	events2 "github.com/smartcontractkit/chainlink-protos/workflows/go/events"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
)

type CapabilityConfig struct {
	ChainID                uint64        `json:"chainId"`
	Network                string        `json:"network"`
	LogTriggerPollInterval time.Duration `json:"logTriggerPollInterval"`
	CREForwarderAddress    string        `json:"creForwarderAddress"`
	ReceiverGasMinimum     uint64        `json:"receiverGasMinimum"`
	NodeAddress            string        `json:"nodeAddress"`
}

func CreateEVMCapabilityConfig(t *testing.T, chainID uint64, network string, duration time.Duration, forwarderAddress common.Address) string {
	readContractConfig := CapabilityConfig{
		ChainID:                chainID,
		Network:                network,
		LogTriggerPollInterval: duration,
		NodeAddress:            "fakeAddressForTesting", // fake address for testing
		CREForwarderAddress:    forwarderAddress.Hex(),
		ReceiverGasMinimum:     4000000,
	}

	configJSON, err := json.Marshal(readContractConfig)
	if err != nil {
		t.Fatalf("failed to marshal evm capability config: %v", err)
	}
	fmt.Println("EVM config :" + string(configJSON))

	readCapabilityConfig := "'''" + string(configJSON) + "'''"
	return readCapabilityConfig
}

func registerWorkflow(t *testing.T, donContext framework.DonContext, workflowName string, compressedBinary []byte, secretsURL string, workflowDon *framework.DON, binaryURL string, configURL string, configBytes []byte) {
	{
		workflowID, err := workflows.GenerateWorkflowID(donContext.EthBlockchain.TransactionOpts().From[:], workflowName, compressedBinary, configBytes, secretsURL)
		require.NoError(t, err)

		err = workflowDon.AddWorkflow(framework.Workflow{
			Name:       workflowName,
			ID:         workflowID,
			Status:     0,
			BinaryURL:  binaryURL,
			ConfigURL:  configURL,
			SecretsURL: secretsURL,
		})
		require.NoError(t, err)
	}
}

func getBeholderLogsForWorkflow(beholderTester beholdertest.Observer, t *testing.T) [][]*events2.LogLine {
	var workflowLogs [][]*events2.LogLine

	userMsgs := beholderTester.Messages(t, "beholder_data_schema", "/cre-events-user-logs/v1")
	if len(userMsgs) > 0 {
		for _, userMsg := range userMsgs {
			userLog := events2.UserLogs{}
			err := proto.Unmarshal(userMsg.Body, &userLog)
			require.NoError(t, err)
			fmt.Printf("Beholder Observer logs: Payload.msg: %v\n", &userLog.LogLines)
			workflowLogs = append(workflowLogs, userLog.LogLines)
		}
	}

	return workflowLogs
}
