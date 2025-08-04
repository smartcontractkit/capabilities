package evm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/smartcontractkit/capabilities/integration_tests/evm/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"

	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/forwarder"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"
)

func Test_WriteReport(t *testing.T) {
	ctx := t.Context()
	beholderTester := beholdertest.NewObserver(t)
	lggr := logger.TestLogger(t)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	readBalancesWithConfigPath, err := filepath.Abs("./workflow")
	require.NoError(t, err)

	wasmFile := filepath.Join(readBalancesWithConfigPath, "evm_write_report.wasm")
	mainFile := filepath.Join(readBalancesWithConfigPath, "main_write_report_wasip1.go")
	utils.CreateWasmBinary(t, mainFile, wasmFile)

	numOfWorkflowNodes := 4
	workflowName := "TestWriteReport"

	setupDon(ctx, t, lggr, wasmFile, numOfWorkflowNodes, workflowName)

	triggerTimeIdenticalConsensusValue := map[string][]string{}
	require.Eventually(t, func() bool {
		lggr.Debugf("oratual working on it...")
		beholderLogs := getBeholderLogsForWorkflow(beholderTester, t)

		for _, logs := range beholderLogs {
			// Expect only one log line
			require.Len(t, logs, 1, "Expected exactly one log line per workflow (it's printed inside the onTrigger() function of the workflow)")
			log := logs[0]

			if strings.Contains(log.Message, "V2 Workflow Execution Result") {
				re := regexp.MustCompile(`trigger time (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z)`)
				matches := re.FindStringSubmatch(log.Message)
				triggerTime := matches[1]
				re = regexp.MustCompile(`consensus value (\d+)`)
				matches = re.FindStringSubmatch(log.Message)
				consensusValue := matches[1]
				triggerTimeIdenticalConsensusValue[triggerTime] = append(triggerTimeIdenticalConsensusValue[triggerTime], consensusValue)
			}
		}

		// Check if we have the expected number of identical consensus values for each trigger time
		for _, consensusValues := range triggerTimeIdenticalConsensusValue {
			if len(consensusValues) == numOfWorkflowNodes {
				if len(consensusValues) == numOfWorkflowNodes && allEqual(consensusValues) {
					return true
				}
			}
		}

		return false
	}, 15*time.Second, 5*time.Second)
}

func setupDon(ctx context.Context, t *testing.T, lggr logger.Logger, workflowURL string, numOfWorkflowNodes int, workflowName string,
) {
	configURL := "config.yaml"
	compressedBinary, base64EncodedCompressedBinary := utils.GetCompressedWorkflowWasm(t, workflowURL)

	data, err := os.ReadFile("./workflow/config.yaml")
	if err != nil {
		lggr.Errorf("oratual 005 failed to read workflow config: %v", err)
	}
	lggr.Infof("oratual 004 Read config.yaml content: %s", string(data))

	syncerFetcherFunc := func(ctx context.Context, messageID string, req capabilities.Request) ([]byte, error) {
		url := req.URL
		switch url {
		case workflowURL:
			return []byte(base64EncodedCompressedBinary), nil
		case configURL:
			return data, nil
		}

		return nil, fmt.Errorf("unknown  url: %s", url)
	}

	donContext := framework.CreateDonContextWithWorkflowRegistry(ctx, t, syncerFetcherFunc, utils.NoopComputeFetcherFactory{})

	address, _, _, err := contract.DeployContract(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)
	lggr.Infof("Deployed contract at address: %s", address.Hex())

	// Instantiate the contract at the deployed keystoneForwaderContractAddress
	keystoneForwaderContractAddress, _, kfcClient, err := forwarder.DeployKeystoneForwarder(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)

	tx, err := kfcClient.Report(donContext.EthBlockchain.TransactionOpts(), keystoneForwaderContractAddress, []byte{}, []byte{}, [][]byte{})
	require.NoError(t, err)

	donContext.EthBlockchain.Backend.Commit()

	receipt, err := donContext.EthBlockchain.Client().TransactionReceipt(ctx, tx.Hash())
	require.NoError(t, err)
	fmt.Println(receipt.Status)

	lggr.Infof("Deployed keystone forwader contract at address: %s", keystoneForwaderContractAddress.Hex())

	_, err = kfcClient.AddForwarder(donContext.EthBlockchain.TransactionOpts(), keystoneForwaderContractAddress)
	require.NoError(t, err)

	//messageEmitter, err := contract.NewContract(address, donContext.EthBlockchain.Client())
	//require.NoError(t, err)

	//tx, err := messageEmitter.EmitMessage(donContext.EthBlockchain.TransactionOpts(), "hola lauti")
	//require.NoError(t, err)
	//lggr.Infof("EmitMessage tx sent: %s", tx.Hash().Hex())

	cronBinary, err := utils.DeployCapability(t, "cron")
	require.NoError(t, err)
	evmBinary, err := utils.DeployCapability(t, "chain_capabilities/evm")
	require.NoError(t, err)

	// Setup workflow DON
	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "EVMLogTriggerWorkflow", NumNodes: numOfWorkflowNodes, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	//// TODO: see if we use this method or the published one
	//workflowDon.AddEthereumWriteTargetNonStandardCapability(keystoneForwaderContractAddress)
	consensusBinary, err := utils.DeployCapability(t, "consensus")

	evmConfig := CreateEVMCapabilityConfig(t, 1337, "evm", 10*time.Second, keystoneForwaderContractAddress)
	workflowDon.AddStandardCapability("evm-capabilities", evmBinary, evmConfig)
	workflowDon.AddStandardCapability("cron-capabilities", cronBinary, utils.GetCronConfig(t, 1))
	workflowDon.AddStandardCapability("consensus-capabilities", consensusBinary, GetConsensusConfig(t, 10000))

	workflowDon.AddOCR3NonStandardCapability()

	targetSink := framework.NewTargetSink("mock-target", "1.0.0")
	workflowDon.AddTargetCapability(targetSink)

	workflowDon.Initialise()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	registerWorkflow(t, donContext, workflowName, compressedBinary, "", workflowDon,
		workflowURL, configURL, data)
}

func allEqual(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] != values[0] {
			return false
		}
	}
	return true
}

//
////func Test_WriteReport_No_DON(t *testing.T) {
////	ctx := t.Context()
////	receiverAddress := common.HexToAddress("0x6D7C976AE064a519bCB37C4361C8990fEA425fA7")
////	var keystoneForwaderContractAddress common.Address
////	var kfc *gethwrappers.KeystoneForwarderTest
////
////	testApp := evm.NewTestAppWithSimulatedBackend(t, evm.TestAppConfig{
////		CustomizeConfigFunc: func(config *chainlink.GeneralConfig, backend evm.SimulatedBackend, transactOptsAccoutns evm.TransactOptsAccounts) {
////			keystoneForwaderContractAddress = deployAndConfigureKeystoneForwader(t, keystoneForwaderContractAddress, kfc, transactOptsAccoutns, backend)
////			applyEVMRelayerConfig(config, transactOptsAccoutns)
////		},
////	})
////	app := testApp.App
////	app.Start(t.Context())
////	defer testApp.App.Stop()
////
////	simulatedBackend := testApp.SimulatedBackend
////	relayers := app.GetRelayers()
////	evmRelayer, err := relayers.Get(commontypes.NewRelayID(simulatedBackend.Network, simulatedBackend.ChainID.String()))
////	require.NoError(t, err)
////
////	evm := createEVMService(t, evmRelayer, simulatedBackend, keystoneForwaderContractAddress)
////	requestMetadata, writeReportRequest := createWriteReportRequest(t, receiverAddress)
////
////	reply, err := evm.WriteReport(ctx, requestMetadata, writeReportRequest)
////	assertWriteReportReply(t, err, reply, receiverAddress)
////
////	reply, err = evm.WriteReport(ctx, requestMetadata, writeReportRequest)
////	assertWriteReportReply(t, err, reply, receiverAddress)
////
////	totalTxs := getTotalNumberOfTXs(ctx, t, testApp.SimulatedBackend)
////	require.Equal(t, 3, totalTxs)
////}
////
////func getTotalNumberOfTXs(ctx context.Context, t *testing.T, simulatedBackend evm.SimulatedBackend) int {
////	client := simulatedBackend.Backend.Client()
////	header, err := client.HeaderByNumber(ctx, nil)
////	require.NoError(t, err)
////
////	latestBlock := int(header.Number.Int64())
////	var totalTxs int = 0
////
////	for i := int(0); i <= latestBlock; i++ {
////		block, err := client.BlockByNumber(ctx, big.NewInt(int64(i)))
////		if err != nil {
////			continue
////		}
////		totalTxs += int(len(block.Transactions()))
////	}
////	return totalTxs
////}
////
////func assertWriteReportReply(t *testing.T, err error, reply *evmcappb.WriteReportReply, receiverAddress common.Address) {
////	require.NoError(t, err)
////	require.NotEmpty(t, reply.TxHash)
////	require.NotEmpty(t, reply.ErrorMessage)
////	require.Contains(t, *reply.ErrorMessage, fmt.Sprintf("Invalid receiver: %s", strings.ToLower(receiverAddress.Hex()[2:])))
////	require.Equal(t, reply.TxStatus, evmcappb.TxStatus_TX_STATUS_SUCCESS)
////	require.Equal(t, *reply.ReceiverContractExecutionStatus, evmcappb.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED)
////}
////
////func applyEVMRelayerConfig(config *chainlink.GeneralConfig, transactOptsAccoutns evm.TransactOptsAccounts) {
////	c := *config
////	fromAddress := types.EIP55AddressFromAddress(transactOptsAccoutns.TransactOpts[1].From)
////	c.EVMConfigs()[0].Workflow.FromAddress = &fromAddress
////	c.EVMConfigs()[0].GasEstimator.PriceMax = assets.NewWei(big.NewInt(50000000))
////}
////
////func deployAndConfigureKeystoneForwader(t *testing.T, keystoneForwaderContractAddress common.Address, kfc *gethwrappers.KeystoneForwarderTest, transactOptsAccoutns evm.TransactOptsAccounts, backend evm.SimulatedBackend) common.Address {
////	var err error = nil
////	keystoneForwaderContractAddress, _, kfc, err = gethwrappers.DeployKeystoneForwarderTest(transactOptsAccoutns.TransactOpts[0], backend.Backend.Client())
////	require.NoError(t, err)
////	backend.Backend.Commit()
////
////	_, err = kfc.AddForwarder(transactOptsAccoutns.Deployer, keystoneForwaderContractAddress)
////	require.NoError(t, err)
////	backend.Backend.Commit()
////	return keystoneForwaderContractAddress
////}
////
////func createWriteReportRequest(t *testing.T, receiverAddress common.Address) (commoncap.RequestMetadata, *evmcappb.WriteReportRequest) {
////	report, err := NewTestReport(t, []byte{})
////	require.NoError(t, err)
////	reportCtx := []byte{42}
////	sigs := [][]byte{{1, 2, 3}}
////
////	repDecoded, err := Decode(report)
////	require.NoError(t, err)
////
////	requestMetadata := commoncap.RequestMetadata{
////		WorkflowID:               repDecoded.WorkflowID,
////		WorkflowOwner:            repDecoded.WorkflowOwner,
////		WorkflowExecutionID:      repDecoded.ExecutionID,
////		WorkflowDonConfigVersion: repDecoded.DONConfigVersion,
////		WorkflowName:             repDecoded.WorkflowName,
////	}
////
////	reportID, err := hex.DecodeString(repDecoded.ReportID)
////	require.NoError(t, err)
////	writeReportRequest := evmcappb.WriteReportRequest{
////		Receiver: receiverAddress[:],
////		Report: &evmcappb.SignedReport{
////			RawReport:     report,
////			ReportContext: reportCtx,
////			Signatures:    sigs,
////			Id:            reportID,
////		},
////		GasConfig: &evmcappb.GasConfig{GasLimit: 10000000000},
////	}
////	return requestMetadata, &writeReportRequest
////}
////
////func createEVMService(t *testing.T, evmRelayer loop.Relayer, simulatedBackend evm.SimulatedBackend, keystoneForwaderContractAddress common.Address) actions.EVM {
////	config := config.Config{
////		ChainID:                simulatedBackend.ChainID.Uint64(),
////		Network:                simulatedBackend.Network,
////		CREForwarderAddress:    keystoneForwaderContractAddress.Hex(),
////		BlockDepth:             100,
////		ReceiverGasMinimum:     2000000,
////		LogTriggerPollInterval: time.Hour,
////	}
////	evmService, err := evmRelayer.EVM()
////	require.NoError(t, err)
////	consensusHandler := mocks.NewConsensusHandler(t)
////	evm, err := actions.NewEVM(config, evmService, logger.TestLogger(t), test.NopBeholderProcessor{}, &monitoring.MessageBuilder{}, consensusHandler)
////	require.NoError(t, err)
////	return evm
////}
////
////func RandomHex(n int) string {
////	byteLen := (n + 1) / 2 // round up if n is odd
////	bytes := make([]byte, byteLen)
////	rand.Read(bytes)
////	hexStr := hex.EncodeToString(bytes)
////	return hexStr[:n]
////}

type ConsensusConfig struct {
	MaximumRequestSizeBytes int `json:"maximumRequestSizeBytes"`
}

func GetConsensusConfig(t *testing.T, maximumRequestSizeBytes int) string {
	config := ConsensusConfig{
		MaximumRequestSizeBytes: maximumRequestSizeBytes,
	}

	jsonConfig, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	return "'" + string(jsonConfig) + "'"
}
