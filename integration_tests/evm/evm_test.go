package evmlogtrigger

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/scylladb/go-reflectx"
	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	"github.com/smartcontractkit/chainlink-evm/pkg/logpoller"
	"github.com/smartcontractkit/chainlink/v2/core/logger"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/require"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"

	"github.com/smartcontractkit/capabilities/integration_tests/evm/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"

	events2 "github.com/smartcontractkit/chainlink-protos/workflows/go/events"
)

// Test_LogTrigger tests the log trigger functionality in the EVM capability.
// It deploys a contract that emits logs, sets up a workflow with that deployed contract to the log trigger, waits for the workflow to be ready,
// emits a log event, and then checks that the workflow processes the log event correctly by counting the number of events logged by beholder.
func Test_LogTrigger(t *testing.T) {
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

	messageEmitter, donContext, workflowDon := setupDon(ctx, t, lggr, wasmFile, abiString, eventName, topic0, numOfWorkflowNodes, workflowName)

	// waiting time to ensure the logTrigger inside the workflow is ready to process messages
	waitUntilLogPollerFiltersArePresent(t, lggr, workflowDon, numOfWorkflowNodes)

	// emitting single event we will be waiting from the workflow's LogTrigger
	messageDataThatWillBeEmitted := "Data for log trigger"
	tx, err := messageEmitter.EmitMessage(donContext.EthBlockchain.TransactionOpts(), messageDataThatWillBeEmitted)
	require.NoError(t, err)
	lggr.Infof("EmitMessage tx sent: %s", tx.Hash().Hex())
	receipt, err := bind.WaitMined(ctx, donContext.EthBlockchain.Client(), tx)
	require.NoError(t, err)
	lggr.Infof("Transaction mined in block: %d", receipt.BlockNumber.Uint64())

	lggr.Info("About to start waiting for workflow logs to be emitted...")
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
	lggr.Infof("Found %d events in beholder logs, as expected", foundEvents)
	t.Fatal("Forcing an error to get the logs printed in the test output")

}

func waitUntilLogPollerFiltersArePresent(t *testing.T, lggr logger.SugaredLogger, workflowDon *framework.DON, workderNodes int) {
	// TODO PLEX-1621: this wait time should be much lower, but in CI needs to be high enough to make log poller ready to work on the logs
	//time.Sleep(10 * time.Second)

	nodes := workflowDon.GetAllNodes()
	for _, node := range nodes {
		config := node.GetConfig()
		database := config.Database()

		url := database.URL()
		//URLs for node: {postgresql  chainlink_dev:insecurepassword localhost:5432 /chainlink_test_1e5fa7a338864502a8f20dd47dbc26d8  false false sslmode=disable  }
		fmt.Println("URLs for node:", url.String())
	}

	results := make(map[int]bool)
	ticker := 5 * time.Second
	timeout := 2 * time.Minute

	lggr.Infof("About to start waiting for all nodes to have expected filters registered, timeout: %.2f seconds", timeout.Seconds())
INNER_LOOP:
	for {
		select {
		case <-time.After(timeout):
			t.Fatalf("timed out, when waiting for %.2f seconds, waiting for all nodes to have expected filters registered", timeout.Seconds())
		case <-time.Tick(ticker):
			if len(results) == len(nodes) {
				lggr.Infof("All %d nodes in DON have expected filters registered", len(nodes))
				break INNER_LOOP
			}

			//for _, node := range nodes {
			//	config := node.GetConfig()
			//	database := config.Database()
			//
			//	url := database.URL()
			//	//URLs for node: {postgresql  chainlink_dev:insecurepassword localhost:5432 /chainlink_test_1e5fa7a338864502a8f20dd47dbc26d8  false false sslmode=disable  }
			//	fmt.Println("URLs for node:", url)
			//}

			for i, node := range nodes {

				config := node.GetConfig()
				database := config.Database()

				url := database.URL()
				//URLs for node: {postgresql  chainlink_dev:insecurepassword localhost:5432 /chainlink_test_1e5fa7a338864502a8f20dd47dbc26d8  false false sslmode=disable  }
				fmt.Println("URLs for node:", url)
				fmt.Println("URLs for node (String):", url.String())
				fmt.Printf("URLs for node (String): user and password: %s\n", url.User.String())

				dbName := strings.TrimPrefix(url.Path, "/")
				port, err := strconv.Atoi(url.Port())
				require.NoError(t, err)

				user := url.User.Username()
				fmt.Printf("URLs for node (String) User name: %s\n", user)
				password, _ := url.User.Password()
				fmt.Printf("URLs for node (String) Password: %s\n", password)

				//nodeIndex, nodeIndexErr := crenode.FindLabelValue(workerNode, crenode.IndexKey)
				//if nodeIndexErr != nil {
				//	return pkgerrors.Wrap(nodeIndexErr, "failed to find node index")
				//}
				//
				//nodeIndexInt, nodeIdxErr := strconv.Atoi(nodeIndex)
				//if nodeIdxErr != nil {
				//	return pkgerrors.Wrap(nodeIdxErr, "failed to convert node index to int")
				//}

				if _, ok := results[i]; ok {
					continue
				}

				lggr.Infof("Checking if all WorkflowRegistry filters are registered for worker node %d", i)
				allFilters, filtersErr := getAllFilters(context.Background(), lggr, big.NewInt(1337), dbName, port, user, password)
				if filtersErr != nil {
					t.Fatalf("Failed to get all filters: %v", filtersErr)
				}

				for _, filter := range allFilters {
					lggr.Infof("Filter found for node %d: Name: %s, EventSigs(%d): %v", i, filter.Name, len(filter.EventSigs), filter.EventSigs)
					//if strings.Contains(filter.Name, "WorkflowRegistry") {
					//	if len(filter.EventSigs) == 6 {
					//		lggr.Infof("Found all WorkflowRegistry filters for node %d", i)
					//		results[i] = true
					//		continue
					//	}
					//
					//	lggr.Infof("Found only %d WorkflowRegistry filters for node %d", len(filter.EventSigs), i)
					//}

					if strings.Contains(filter.Name, "-evm-log-trigger") {
						if len(filter.EventSigs) == 1 {
							lggr.Infof("Found all filters for logTrigger for node %d", i)
							results[i] = true
							continue
						}

						lggr.Infof("Found only %d WorkflowRegistry filters for node %d", len(filter.EventSigs), i)
					}
				}
			}

			// return if we have results for all nodes, don't wait for next tick
			if len(results) == workderNodes {
				lggr.Infof("All %d nodes in DON have expected filters registered2", workderNodes)
				break INNER_LOOP
			}
		}
	}
}

func NewORM(logger logger.Logger, chainID *big.Int, dbName string, externalPort int, user string, password string) (logpoller.ORM, *sqlx.DB, error) {
	dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", "localhost", externalPort, user, password, dbName)
	logger.Infof("Connecting to database %s", dsn)
	//dsn := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable", "127.0.0.1", externalPort, "", "", dbName)
	db, err := sqlx.Open("postgres", dsn)
	if err != nil {
		return nil, db, fmt.Errorf("failed to connect to database: %w", err)
	}

	db.MapperFunc(reflectx.CamelToSnakeASCII)
	return logpoller.NewORM(chainID, db, logger), db, nil
}

func getAllFilters(ctx context.Context, logger logger.Logger, chainID *big.Int, dbName string, externalPort int, user string, password string) (map[string]logpoller.Filter, error) {
	orm, db, err := NewORM(logger, chainID, dbName, externalPort, user, password)
	if err != nil {
		return nil, fmt.Errorf("failed to create ORM: %v", err)
	}

	defer db.Close()
	return orm.LoadFilters(ctx)
}

type runtimeConfig struct {
	Addresses []string `yaml:"addresses"`
	Topics    []struct {
		Values []string `yaml:"values"`
	} `yaml:"topics"`
	Abi   string `yaml:"abi,omitempty"`
	Event string `yaml:"event,omitempty"`
}

func setupDon(ctx context.Context, t *testing.T, lggr logger.Logger, workflowURL string, abiString string, eventName string, topic0 common.Hash, numOfWorkflowNodes int, workflowName string) (*contract.Contract, framework.DonContext, *framework.DON) {
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

	return messageEmitter, donContext, workflowDon
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
