package evmlogtrigger

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	"github.com/smartcontractkit/chainlink/v2/core/logger"

	"github.com/stretchr/testify/require"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"

	"github.com/smartcontractkit/capabilities/integration_tests/evm/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/trigger"

	events2 "github.com/smartcontractkit/chainlink-protos/workflows/go/events"
)

// Test_LogTrigger tests the log trigger functionality in the EVM capability.
// It deploys a contract that emits logs, sets up a workflow with that deployed contract to the log trigger, waits for the workflow to be ready,
// emits a log event, and then checks that the workflow processes the log event correctly by counting the number of events logged by beholder.
func Test_LogTrigger(t *testing.T) {
	t.Skip("PRODCRE-833 Needs to be fixed")
	ctx := t.Context()
	beholderTester := beholdertest.NewObserver(t)
	lggr, obs := logger.TestLoggerObserved(t, zapcore.InfoLevel)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	// prepping input params
	workflowPath, err := filepath.Abs("./workflow")
	require.NoError(t, err)
	mainFile := filepath.Join(workflowPath, "main_logtrigger_wasip1.go")
	wasmFile := filepath.Join(utils.CapabilitiesDir, "evm_logTrigger.wasm") // forcing cleanup on defer
	utils.CreateWasmBinary(t, mainFile, wasmFile)
	abiBytes, err := os.ReadFile("./contract/MessageEmitter.abi")
	require.NoError(t, err)
	abiString := string(abiBytes)
	parsedABI, err := abi.JSON(strings.NewReader(abiString))
	require.NoError(t, err)
	eventName := "MessageEmitted"
	event := parsedABI.Events[eventName]
	topic0 := event.ID

	numOfWorkflowNodes := 4
	workflowName := "TestWf"

	messageEmitter, donContext := setupDon(ctx, t, lggr, wasmFile, abiString, eventName, topic0, numOfWorkflowNodes, workflowName)

	waitUntilLogPollerFiltersArePresent(t, obs, lggr, numOfWorkflowNodes)

	// emitting single event we will be waiting from the workflow's LogTrigger
	messageDataThatWillBeEmitted := "Data for log trigger"
	tx, err := messageEmitter.EmitMessage(donContext.EthBlockchain.TransactionOpts(), messageDataThatWillBeEmitted)
	require.NoError(t, err)
	lggr.Infof("EmitMessage tx sent: %s", tx.Hash().Hex())
	receipt, err := bind.WaitMined(ctx, donContext.EthBlockchain.Client(), tx)
	require.NoError(t, err)
	lggr.Infof("Transaction mined in block: %d", receipt.BlockNumber.Uint64())

	// assertion to validate we get the expected number of events in beholder logs
	foundEvents := 0
	require.Eventually(t, func() bool {
		lggr.Info("Waiting for workflow logs to be emitted...")
		workflowLogs := getBeholderLogsForWorkflow(beholderTester, t)
		// Wait until we have the logs for all workflows
		if len(workflowLogs) < numOfWorkflowNodes {
			lggr.Infof("Workflow logs not emitted, current size: %d, expected: %d", len(workflowLogs), numOfWorkflowNodes)
			return false
		}

		for _, logs := range workflowLogs {
			// Expect only one log line
			require.Len(t, logs, 1, "Expected exactly one log line per workflow (it's printed inside the onTrigger() function of the workflow)")
			log := logs[0]
			if strings.Contains(log.GetMessage(), messageDataThatWillBeEmitted) {
				foundEvents++
			}
		}
		return foundEvents == numOfWorkflowNodes
	}, 60*time.Second, // test takes in average 24 seconds to complete locally
		1*time.Second,
		"Expected to find %d events, but found %d", numOfWorkflowNodes, foundEvents)
}

func waitUntilLogPollerFiltersArePresent(t *testing.T, obs *observer.ObservedLogs, lggr logger.Logger, numOfWorkflowNodes int) {
	require.Eventually(t, func() bool {
		logs := obs.
			FilterMessageSnippet("Inserted filter").
			Filter(func(e observer.LoggedEntry) bool {
				for _, ctxField := range e.Context {
					if ctxField.Key == "name" && strings.Contains(ctxField.String, trigger.SuffixLogTriggerFilterID) {
						return true
					}
				}
				return false
			}).
			All()
		return len(logs) == numOfWorkflowNodes
	}, 2*time.Minute,
		3*time.Second, "Expected to find %d log poller filters", numOfWorkflowNodes)
}

type runtimeConfig struct {
	Addresses []string `yaml:"addresses"`
	Topics    []struct {
		Values []string `yaml:"values"`
	} `yaml:"topics"`
	Abi   string `yaml:"abi,omitempty"`
	Event string `yaml:"event,omitempty"`
}

func setupDon(ctx context.Context, t *testing.T, lggr logger.Logger, workflowURL string, abiString string, eventName string, topic0 common.Hash, numOfWorkflowNodes int, workflowName string) (*contract.Contract, framework.DonContext) {
	configURL := "config.yaml"
	compressedBinary, base64EncodedCompressedBinary := utils.GetCompressedWorkflowWasm(t, workflowURL)

	urlToConfigBytes := map[string][]byte{}

	syncerFetcherFunc := func(ctx context.Context, messageID string, req capabilities.Request) ([]byte, error) {
		url := req.URL
		switch url {
		case workflowURL:
			return []byte(base64EncodedCompressedBinary), nil
		case configURL:
			return urlToConfigBytes[configURL], nil
		}
		return nil, fmt.Errorf("unknown  url: %s", url)
	}

	donContext := framework.CreateDonContextWithWorkflowRegistry(ctx, t, syncerFetcherFunc, utils.NoopComputeFetcherFactory{})

	address, _, _, err := contract.DeployContract(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)
	lggr.Debugf("Deploy contract address: %s", address)

	runtimeCfg := runtimeConfig{
		Addresses: []string{address.Hex()},
		Topics: []struct {
			Values []string `yaml:"values"`
		}{
			{Values: []string{topic0.Hex()}},
		},
		Abi:   abiString,
		Event: eventName,
	}
	data, err := yaml.Marshal(runtimeCfg)
	require.NoError(t, err)
	urlToConfigBytes[configURL] = data

	//Instantiate the contract at the deployed address
	messageEmitter, err := contract.NewContract(address, donContext.EthBlockchain.Client())
	require.NoError(t, err)

	evmBinary, err := utils.DeployCapability(t, "chain_capabilities/evm")
	require.NoError(t, err)

	// Setup workflow DON
	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "EVMLogTriggerWorkflow", NumNodes: numOfWorkflowNodes, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	evmConfig := CreateEvmCapabilityConfig(t, 1337, "evm", 3*time.Second)
	workflowDon.AddStandardCapability("evm-capabilities", evmBinary, evmConfig)

	workflowDon.AddOCR3NonStandardCapability()

	workflowDon.Initialise()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	registerWorkflow(t, donContext, workflowName, compressedBinary, "", workflowDon,
		workflowURL, configURL, urlToConfigBytes[configURL])

	return messageEmitter, donContext
}

func registerWorkflow(t *testing.T, donContext framework.DonContext, workflowName string, compressedBinary []byte,
	secretsURL string, workflowDon *framework.DON, binaryURL string, configURL string, configBytes []byte) {
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

type EVMCapabilityConfig struct {
	ChainID                uint64        `json:"chainId"`
	Network                string        `json:"network"`
	LogTriggerPollInterval time.Duration `json:"logTriggerPollInterval"`
	CREForwarderAddress    string        `json:"creForwarderAddress"`
	ReceiverGasMinimum     uint64        `json:"receiverGasMinimum"`
	NodeAddress            string        `json:"nodeAddress"`
}

func CreateEvmCapabilityConfig(t *testing.T, chainID uint64, network string, duration time.Duration) string {
	readContractConfig := EVMCapabilityConfig{
		ChainID:                chainID,
		Network:                network,
		LogTriggerPollInterval: duration,
		CREForwarderAddress:    "1234567890abcdef1234567890abcdef12345678", //fake address for testing
		ReceiverGasMinimum:     1,
		NodeAddress:            "fakeAddressForTesting", //fake address for testing
	}

	configJSON, err := json.Marshal(readContractConfig)
	if err != nil {
		t.Fatalf("failed to marshal evm capability config: %v", err)
	}

	readCapabilityConfig := "'''" + string(configJSON) + "'''"
	return readCapabilityConfig
}
