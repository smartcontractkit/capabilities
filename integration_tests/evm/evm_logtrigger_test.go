package evmlogtrigger

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	"github.com/smartcontractkit/chainlink/v2/core/logger"

	"github.com/stretchr/testify/require"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"

	"github.com/smartcontractkit/capabilities/integration_tests/evm/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/trigger"

	events2 "github.com/smartcontractkit/chainlink-protos/workflows/go/events"
)

// NonMatching is a constant string used to denote non-matching events in tests. This literal value must not be printed by the workflow
// when processing matching events, to allow assertions that non-matching events were ignored.
const NonMatching = "NON MATCHING"

// DeployContractsFunc deploys the necessary contracts for the test and returns their addresses.
type DeployContractsFunc func(t *testing.T, donContext framework.DonContext) []common.Address

// BuildRuntimeConfigFunc builds a RuntimeConfig given event details, or the current test case parameters.
type BuildRuntimeConfigFunc func(eventName string, abiString string, topic0Hex string) RuntimeConfig

// EmitEventsFunc emits events using the provided contract and DON context, returning the transactions
// that should be considered as "matching" for the assertions. Implementations are free to emit
// additional non-matching events prior to emitting the matching ones.
type EmitEventsFunc func(t *testing.T, messageEmitters []*contract.Contract, donContext framework.DonContext) []*types.Transaction

// defaultDeployContracts helper fn to deploy a single instance of the contract.
func defaultDeployContracts(t *testing.T, donContext framework.DonContext) []common.Address {
	address, _, _, err := contract.DeployContract(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)
	return []common.Address{address}
}

type logTriggerTestCase struct {
	workflowName             string
	eventName                string
	matchingMessages         []string
	deployContractsFn        DeployContractsFunc
	buildRuntimeConfigFn     BuildRuntimeConfigFunc
	emitEventsFn             EmitEventsFunc
	verifyNonMatchingIgnored bool
}

func runLogTriggerTests(t *testing.T, testCases []logTriggerTestCase) {
	for _, tc := range testCases {
		t.Run(tc.workflowName, func(t *testing.T) {
			assertLogTriggerWorks(t, tc.eventName, tc.workflowName, tc.matchingMessages,
				tc.deployContractsFn, tc.buildRuntimeConfigFn, tc.emitEventsFn, tc.verifyNonMatchingIgnored)
		})
	}
}

// Test_SimpleLogTrigger tests the log trigger with simple matching scenarios for each confidence level.
func Test_SimpleLogTrigger(t *testing.T) {
	var testCases []logTriggerTestCase
	// adding test cases for each confidence level
	confidenceLevelMap := map[int32]string{
		0: "SAFE",
		1: "LATEST",
		2: "FINALIZED",
	}
	for confidence, confidenceLabel := range confidenceLevelMap {
		message := fmt.Sprintf("Data for log trigger, confidence %s", confidenceLabel)
		matchingMsgs := []string{message}
		tc := logTriggerTestCase{
			workflowName:      fmt.Sprintf("TestSimpleLogTrigger_%s", confidenceLabel),
			eventName:         "MessageEmitted",
			matchingMessages:  matchingMsgs,
			deployContractsFn: defaultDeployContracts,
			buildRuntimeConfigFn: func(eventName, abiString, topic0Hex string) RuntimeConfig {
				return RuntimeConfig{
					Topics: []struct {
						Values []string `yaml:"values"`
					}{
						{Values: []string{topic0Hex}},
					},
					Confidence: confidence,
					Abi:        abiString,
					Event:      eventName,
				}
			},
			emitEventsFn: func(t *testing.T, messageEmitters []*contract.Contract, donContext framework.DonContext) []*types.Transaction {
				messageEmitter := messageEmitters[0]
				tx, err := messageEmitter.EmitMessage(donContext.EthBlockchain.TransactionOpts(), message)
				require.NoError(t, err)
				return []*types.Transaction{tx}
			},
			verifyNonMatchingIgnored: false,
		}
		testCases = append(testCases, tc)
	}
	runLogTriggerTests(t, testCases)
}

// Test_LogTriggerMultipleTopics tests the log trigger with multiple topic filters while also checking that non-matching events are ignored.
func Test_LogTriggerMultipleTopics(t *testing.T) {
	testCases := []logTriggerTestCase{
		{
			workflowName:      "TestLogTrigger_topic2_filter_LATEST",
			eventName:         "MultiTopicEmitted",
			matchingMessages:  []string{"Data for log trigger using topic2 only filter"},
			deployContractsFn: defaultDeployContracts,
			buildRuntimeConfigFn: func(eventName, abiString, topic0Hex string) RuntimeConfig {
				return RuntimeConfig{
					Topics: []struct {
						Values []string `yaml:"values"`
					}{
						{Values: []string{topic0Hex}},
						{Values: []string{fmt.Sprintf("0x%064x", uint64(42))}}, // 42, == 0x000000000000000000000000000000000000000000000000000000000000002a
					},
					Confidence: 1, // latest
					Abi:        abiString,
					Event:      eventName,
				}
			},
			emitEventsFn: func(t *testing.T, messageEmitters []*contract.Contract, donContext framework.DonContext) []*types.Transaction {
				messageEmitter := messageEmitters[0]
				var txs []*types.Transaction
				// first emit a clearly non-matching event (different topic2)
				nonMatchingTopic2 := big.NewInt(999)
				topic3 := big.NewInt(999)
				topic4 := big.NewInt(888)
				// adding the non-matching marker guarantees we can assert it is not processed
				nonMatchingMessage := fmt.Sprintf("Data for log trigger %s topic2 only filter", NonMatching)
				txNonMatch, err := messageEmitter.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), nonMatchingTopic2, topic3, topic4, nonMatchingMessage)
				require.NoError(t, err)
				txs = append(txs, txNonMatch)

				// then emit the matching event (topic2 == 42)
				matchingTopic2 := big.NewInt(42)
				txMatch, err := messageEmitter.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), matchingTopic2, topic3, topic4,
					"Data for log trigger using topic2 only filter")
				require.NoError(t, err)
				txs = append(txs, txMatch)

				return txs
			},
			verifyNonMatchingIgnored: true,
		},
		{
			workflowName:      "TestLogTrigger_topic2_and_topic4_filter_LATEST",
			eventName:         "MultiTopicEmitted",
			matchingMessages:  []string{"Data for log trigger using topic2 and topic 4, but not 3, filter"},
			deployContractsFn: defaultDeployContracts,
			buildRuntimeConfigFn: func(eventName, abiString, topic0Hex string) RuntimeConfig {
				return RuntimeConfig{
					Topics: []struct {
						Values []string `yaml:"values"`
					}{
						{Values: []string{topic0Hex}},
						{Values: []string{fmt.Sprintf("0x%064x", uint64(42))}},
						{},
						{Values: []string{fmt.Sprintf("0x%064x", uint64(23))}},
					},
					Confidence: 1, // latest
					Abi:        abiString,
					Event:      eventName,
				}
			},
			emitEventsFn: func(t *testing.T, messageEmitters []*contract.Contract, donContext framework.DonContext) []*types.Transaction {
				messageEmitter := messageEmitters[0]
				var txs []*types.Transaction

				// emit one event that should not match (topic4 != 23)
				topic2 := big.NewInt(42)
				topic3 := big.NewInt(999)
				nonMatchingTopic4 := big.NewInt(999)
				// adding the non-matching marker guarantees we can assert it is not processed
				nonMatchingMessage := fmt.Sprintf("Data for log trigger %s topic4 filter", NonMatching)
				txNonMatch, err := messageEmitter.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), topic2, topic3, nonMatchingTopic4,
					nonMatchingMessage)
				require.NoError(t, err)
				txs = append(txs, txNonMatch)

				// emit the matching event (topic2 == 42 and topic4 == 23)
				matchingTopic4 := big.NewInt(23)
				txMatch, err := messageEmitter.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), topic2, topic3, matchingTopic4,
					"Data for log trigger using topic2 and topic 4, but not 3, filter")
				require.NoError(t, err)
				txs = append(txs, txMatch)

				return txs
			},
			verifyNonMatchingIgnored: true,
		},
	}

	runLogTriggerTests(t, testCases)
}

// Test_LogTriggerMultipleAddressesAndTopics tests the log trigger with events emitted from multiple contract addresses
// dismissing any non-matching events.
func Test_LogTriggerMultipleAddressesAndTopics(t *testing.T) {
	message1 := "Data for log trigger from contract 1 (topic 2 = 42)"
	message2 := "Data for log trigger from contract 2 (topic 2 = 23)"
	matchingMsgs := []string{message1, message2}

	testCases := []logTriggerTestCase{
		{
			workflowName:     "TestLogTrigger_MultipleAddresses_LATEST",
			eventName:        "MultiTopicEmitted",
			matchingMessages: matchingMsgs,
			deployContractsFn: func(t *testing.T, donContext framework.DonContext) []common.Address {
				// deploy two instances of the same contract for testing multiple addresses emissions in the log trigger
				addrs := make([]common.Address, 0, 2)
				for i := 0; i < 2; i++ {
					addr, _, _, err := contract.DeployContract(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
					require.NoError(t, err)
					addrs = append(addrs, addr)
				}
				return addrs
			},
			buildRuntimeConfigFn: func(eventName, abiString, topic0Hex string) RuntimeConfig {
				return RuntimeConfig{
					Topics: []struct {
						Values []string `yaml:"values"`
					}{
						{Values: []string{topic0Hex}},
						{Values: []string{
							fmt.Sprintf("0x%064x", uint64(42)),
							fmt.Sprintf("0x%064x", uint64(23)),
						}},
					},
					Confidence: 1, // LATEST
					Abi:        abiString,
					Event:      eventName,
				}
			},
			emitEventsFn: func(t *testing.T, messageEmitters []*contract.Contract, donContext framework.DonContext) []*types.Transaction {
				require.Len(t, messageEmitters, 2, "expected two message emitters (two contracts)")
				var txs []*types.Transaction

				topic3 := big.NewInt(999)
				topic4 := big.NewInt(888)

				// from contract 1: emit + non-matching multi-topic event usint
				me1 := messageEmitters[0]
				matchingTopic2FirstContract := big.NewInt(42)
				tx1, err := me1.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), matchingTopic2FirstContract, topic3, topic4,
					message1)
				require.NoError(t, err)
				txs = append(txs, tx1)

				nonMatchingTopic2FirstContract := big.NewInt(999)
				nonMatchingMessage := fmt.Sprintf("Data for log trigger %s multipletopic contract1", NonMatching)
				tx1NonMatch, err := me1.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), nonMatchingTopic2FirstContract, topic3, topic4, nonMatchingMessage)
				require.NoError(t, err)
				txs = append(txs, tx1NonMatch)

				//from contract 2: emit + another non-matching multi-topic event
				me2 := messageEmitters[1]
				matchingTopic2SecondContract := big.NewInt(23)
				tx2, err := me2.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), matchingTopic2SecondContract, topic3, topic4,
					message2)
				require.NoError(t, err)
				txs = append(txs, tx2)

				nonMatchingTopic2SecondContract := big.NewInt(888)
				nonMatchingMessage2 := fmt.Sprintf("Data for log trigger %s multipletopic contract2", NonMatching)
				tx2NonMatch, err := me2.EmitMultiTopic(donContext.EthBlockchain.TransactionOpts(), nonMatchingTopic2SecondContract, topic3, topic4, nonMatchingMessage2)
				require.NoError(t, err)
				txs = append(txs, tx2NonMatch)

				return txs
			},
			verifyNonMatchingIgnored: true,
		},
	}

	runLogTriggerTests(t, testCases)
}

func assertLogTriggerWorks(t *testing.T, eventName string, workflowName string, matchingMessages []string,
	deployContractsFn DeployContractsFunc,
	buildConfigFn BuildRuntimeConfigFunc,
	emitEventsFn EmitEventsFunc,
	verifyNonMatchingIgnored bool) {
	ctx := t.Context()
	beholderTester := beholdertest.NewObserver(t)
	lggr, obs := logger.TestLoggerObserved(t, zapcore.InfoLevel) // change this to debug to print all logs from the trigger/log poller if needed to debug
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	workflowPath, err := filepath.Abs("./workflow")
	require.NoError(t, err)
	wfFileName := "main_logtrigger_wasip1.go"
	mainFile := filepath.Join(workflowPath, wfFileName)
	wasmFilename := wfFileName[:len(wfFileName)-3] + ".wasm"
	wasmFile := filepath.Join(utils.CapabilitiesDir, wasmFilename)
	utils.CreateWasmBinary(t, mainFile, wasmFile)

	abiBytes, err := os.ReadFile("./contract/MessageEmitter.abi")
	require.NoError(t, err)
	abiString := string(abiBytes)
	parsedABI, err := abi.JSON(strings.NewReader(abiString))
	require.NoError(t, err)
	event := parsedABI.Events[eventName]
	topic0 := event.ID

	numOfWorkflowNodes := 4

	runtimeCfg := buildConfigFn(eventName, abiString, topic0.Hex())

	lggr.Infof("Setting up DON and workflow for log trigger...")
	messageEmitters, donContext := setupDon(ctx, t, lggr, wasmFile, numOfWorkflowNodes, workflowName, runtimeCfg, deployContractsFn)
	lggr.Infof("Waiting for log poller filters to be present for test...")
	waitUntilLogPollerFiltersArePresent(t, obs, numOfWorkflowNodes)
	lggr.Infof("Log poller filters are present for test.")

	// Emit events according to the chosen strategy (can be swapped for other scenarios).
	matchingTxs := emitEventsFn(t, messageEmitters, donContext)
	for i, tx := range matchingTxs {
		lggr.Infof("Message %d emitted in tx: %s", i, tx.Hash().Hex())
		receipt, err := bind.WaitMined(ctx, donContext.EthBlockchain.Client(), tx)
		require.NoError(t, err)
		lggr.Infof("Transaction %d mined in block: %d", i, receipt.BlockNumber.Uint64())
	}

	foundEventsByMessage := make(map[string]int, len(matchingMessages))
	for _, msg := range matchingMessages {
		foundEventsByMessage[msg] = 0
	}

	// assertion to validate we get the expected number of events in beholder logs
	lggr.Infof("Waiting for workflow logs to be emitted for test...")
	require.Eventually(t, func() bool {
		// reset counts on each poll as beholder logs are re-fetched entirely each time
		for msg := range foundEventsByMessage {
			foundEventsByMessage[msg] = 0
		}

		workflowLogs := getBeholderLogsForWorkflow(beholderTester, t)
		if len(workflowLogs) < numOfWorkflowNodes {
			lggr.Infof("Workflow logs not emitted yet for test, current size: %d, expected: %d", len(workflowLogs), numOfWorkflowNodes)
			return false
		}

		for index, logs := range workflowLogs {
			require.Len(t, logs, 1, "Expected exactly one log line per workflow for test, failing.")
			log := logs[0]
			logMessage := log.GetMessage()
			lggr.Infow("Beholder log line", "index", index, "message", logMessage, "nodeTimestamp", log.GetNodeTimestamp())
			for _, matchingMessage := range matchingMessages {
				if strings.Contains(logMessage, matchingMessage) {
					foundEventsByMessage[matchingMessage]++
					lggr.Infow("Log emitted message contains message", "matchingMessage", matchingMessage, "count", foundEventsByMessage[matchingMessage], "numOfWorkflowNodes", numOfWorkflowNodes)
				}
			}
			if verifyNonMatchingIgnored {
				// For scenarios where we purposely emitted non-matching events, assert that
				// no unexpected messages (e.g. non-matching payloads) show up in the workflow logs.
				// This is a coarse-grained check: if we ever evolve the workflow to log the
				// raw topics or payloads of non-matching events, we should refine this assertion
				// to be more specific.
				require.NotContains(t, logMessage, NonMatching, "Non-matching events should not be processed by the log trigger/workflow.")
			}
		}

		// remove any messages that have already met the expected count from the pending map
		for msg, found := range foundEventsByMessage {
			if found == numOfWorkflowNodes {
				lggr.Infof("Partial success: found all expected events %d for message: %q, deleting entry of pending logs to look at",
					numOfWorkflowNodes, msg)
				delete(foundEventsByMessage, msg)
			}
		}
		return len(foundEventsByMessage) == 0
	}, 90*time.Second, 2*time.Second,
		"Expected to find %d matching events, but found: %+v", numOfWorkflowNodes, foundEventsByMessage)

	// Verify ACKs for each matching delivery. BaseTrigger logs Infow("Event ACK", "eventID", ...).
	//
	// We locate ACKs by tx hash embedded in eventID; for each matching message there must be a
	// tx in matchingTxs whose log carried that message.
	require.Eventually(t, func() bool {
		require.NotEmpty(t, matchingMessages)
		matchCount := 0
		for _, msg := range matchingMessages {
			txForMsg := findTxForMatchingMessage(matchingTxs, msg)
			require.NotNil(t, txForMsg, "no emitted tx found for matching message %q (emitEventsFn must return txs for matching emits)", msg)
			txHex := strings.TrimPrefix(strings.ToLower(txForMsg.Hash().Hex()), "0x")
			for _, log := range obs.FilterMessageSnippet("Event ACK").All() {
				if eventAckLogContainsTxHash(log, txHex) {
					matchCount++
					t.Logf("found matching ACK log for message %q tx %s eventID=%v", msg, txForMsg.Hash().Hex(), log.ContextMap()["eventID"])
					break
				}
			}
		}
		expected := len(matchingMessages)
		t.Logf("ACK log matchCount=%d, expected=%d (one per matching workflow message)", matchCount, expected)
		return matchCount == expected
	}, 90*time.Second, 1*time.Second, "expected ACK logs for each matching workflow message")
}

// findTxForMatchingMessage returns the first tx whose corresponding receipt log should carry the
// given message payload (used to pair matchingMessages with txs for ACK lookup).
func findTxForMatchingMessage(txs []*types.Transaction, matchingMsg string) *types.Transaction {
	for _, tx := range txs {
		if tx == nil {
			continue
		}
		if bytesContainsEmitPayload(tx, matchingMsg) {
			return tx
		}
	}
	return nil
}

func bytesContainsEmitPayload(tx *types.Transaction, substr string) bool {
	if tx == nil {
		return false
	}
	d := tx.Data()
	if len(d) == 0 || len(substr) == 0 {
		return false
	}
	return strings.Contains(string(d), substr)
}

// eventAckLogContainsTxHash reports whether a zap observed "Event ACK" log's structured
// eventID field references txHex (lowercase, no 0x prefix). Composite event IDs embed the tx hash.
func eventAckLogContainsTxHash(log observer.LoggedEntry, txHexLowerNo0x string) bool {
	raw, ok := log.ContextMap()["eventID"]
	if !ok || raw == nil {
		return false
	}
	ev, ok := raw.(string)
	if !ok {
		ev = fmt.Sprint(raw)
	}
	evNorm := strings.TrimPrefix(strings.ToLower(ev), "0x")
	return strings.Contains(evNorm, txHexLowerNo0x)
}

func waitUntilLogPollerFiltersArePresent(t *testing.T, obs *observer.ObservedLogs, numOfWorkflowNodes int) {
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
			t.Logf("Beholder Observer logs: Payload.msg: %v\n", &userLog.LogLines)
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
	DeltaStage             time.Duration `json:"deltaStage"`
	IsLocal                bool          `json:"isLocal"`
}

func CreateEvmCapabilityConfig(t *testing.T, chainID uint64, network string, duration time.Duration) string {
	readContractConfig := EVMCapabilityConfig{
		ChainID:                chainID,
		Network:                network,
		LogTriggerPollInterval: duration,
		CREForwarderAddress:    "1234567890abcdef1234567890abcdef12345678", //fake address for testing
		ReceiverGasMinimum:     1,
		NodeAddress:            "fakeAddressForTesting", //fake address for testing
		DeltaStage:             time.Second,
		IsLocal:                true, //bypass transmission scheduler initialization, since we don't use write report in our test anyway
	}

	configJSON, err := json.Marshal(readContractConfig)
	if err != nil {
		t.Fatalf("failed to marshal evm capability config: %v", err)
	}

	readCapabilityConfig := "'''" + string(configJSON) + "'''"
	return readCapabilityConfig
}

func setupDon(ctx context.Context, t *testing.T, lggr logger.Logger, workflowURL string, numOfWorkflowNodes int,
	workflowName string, config RuntimeConfig, deployContractsFn DeployContractsFunc) ([]*contract.Contract, framework.DonContext) {
	configURL := workflowName + "_config.yaml"
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

	addresses := deployContractsFn(t, donContext)
	config.Addresses = make([]string, 0, len(addresses))
	messageEmitters := make([]*contract.Contract, 0, len(addresses))
	for _, addr := range addresses {
		lggr.Infof("Deploy contract address: %s", addr)
		config.Addresses = append(config.Addresses, addr.Hex())

		messageEmitter, err := contract.NewContract(addr, donContext.EthBlockchain.Client())
		require.NoError(t, err)
		messageEmitters = append(messageEmitters, messageEmitter)
	}

	data, err := yaml.Marshal(config)
	require.NoError(t, err)
	t.Logf("Runtime yaml config:\n%s\n", string(data))
	urlToConfigBytes[configURL] = data

	evmBinary, err := utils.DeployCapability(t, "chain_capabilities/evm")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: workflowName + "DonName", NumNodes: numOfWorkflowNodes, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	evmConfig := CreateEvmCapabilityConfig(t, 1337, "evm", 3*time.Second)
	workflowDon.AddStandardCapability("evm-capabilities", evmBinary, evmConfig)

	workflowDon.AddOCR3NonStandardCapability()
	workflowDon.Initialise()

	require.NoError(t, workflowDon.Start(t.Context()))
	t.Cleanup(func() {
		if err := workflowDon.Close(); err != nil &&
			!strings.Contains(err.Error(), "stopped") {
			require.NoError(t, err)
		}
	})

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	registerWorkflow(t, donContext, workflowName, compressedBinary, "", workflowDon,
		workflowURL, configURL, urlToConfigBytes[configURL])

	return messageEmitters, donContext
}
