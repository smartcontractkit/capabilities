package evm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/smartcontractkit/capabilities/integration_tests/evm/contract"
	"gopkg.in/yaml.v3"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

// Test_LogTrigger tests the log trigger functionality in the EVM capability.
// It deploys a contract that emits logs, sets up a workflow with that deployed contract to the log trigger, waits for the workflow to be ready,
// emits a log event, and then checks that the workflow processes the log event correctly by counting the number of events logged by beholder.
func Test_LogTrigger(t *testing.T) {
	//t.Skip("Flaky Test: https://github.com/smartcontractkit/capabilities/actions/runs/16374733824/job/46271708609")
	ctx := t.Context()
	beholderTester := beholdertest.NewObserver(t)
	lggr := logger.TestLogger(t)
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

	messageEmitter, donContext := setupLTDon(ctx, t, lggr, wasmFile, abiString, eventName, topic0, numOfWorkflowNodes, workflowName)

	// waiting time to ensure the logTrigger inside the workflow is ready to process messages
	// TODO PLEX-1621: this wait time should be much lower, but in CI needs to be high enough to make log poller ready to work on the logs
	time.Sleep(100 * time.Second)

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

type logTriggerWFRuntimeConfig struct {
	Addresses []string `yaml:"addresses"`
	Topics    []struct {
		Values []string `yaml:"values"`
	} `yaml:"topics"`
	Abi   string `yaml:"abi,omitempty"`
	Event string `yaml:"event,omitempty"`
}

func setupLTDon(ctx context.Context, t *testing.T, lggr logger.Logger, workflowURL string, abiString string, eventName string, topic0 common.Hash, numOfWorkflowNodes int, workflowName string) (*contract.Contract, framework.DonContext) {
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

	runtimeCfg := logTriggerWFRuntimeConfig{
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

	// Instantiate the contract at the deployed address
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

	evmConfig := CreateEVMCapabilityConfig(t, 1337, "evm", 3*time.Second, common.HexToAddress("1234567890abcdef1234567890abcdef12345678"))
	workflowDon.AddStandardCapability("evm-capabilities", evmBinary, evmConfig)

	workflowDon.AddOCR3NonStandardCapability()

	workflowDon.Initialise()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	registerWorkflow(t, donContext, workflowName, compressedBinary, "", workflowDon,
		workflowURL, configURL, urlToConfigBytes[configURL])

	return messageEmitter, donContext
}
