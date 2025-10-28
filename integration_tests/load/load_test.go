package load

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	common2 "github.com/ethereum/go-ethereum/common" //nolint:depguard
	"github.com/pelletier/go-toml"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/integration_tests/por/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/custmsg"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ratelimit"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm/host"
	kcr "github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/capabilities_registry_1_1_0"
	"github.com/smartcontractkit/chainlink-evm/pkg/assets"
	"github.com/smartcontractkit/chainlink-evm/pkg/testutils"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/compute"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/webapi"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"
	"github.com/smartcontractkit/chainlink/v2/core/services/registrysyncer"
)

const commandOverrideForCustomComputeAction = "__builtin_custom-compute-action"

var defaultConfig = compute.Config{
	ServiceConfig: webapi.ServiceConfig{
		RateLimiter: ratelimit.RateLimiterConfig{
			GlobalRPS:      100.0,
			GlobalBurst:    100,
			PerSenderRPS:   100.0,
			PerSenderBurst: 100,
		},
	},
}

type ReadContractConfig struct {
	ChainID uint64 `json:"chainId"`
	Network string `json:"network"`
}

type readBalancesConfig struct {
	ChainID                      string `json:"chainId"`
	BalanceReaderContractAddress string
	ConsumerAddress              string
	WriteTargetCapabilityID      string
	Addresses                    []string
	CronSchedule                 string
}

type NullFetcherFactory struct{}

var _ compute.FetcherFactory = &NullFetcherFactory{}

func (n NullFetcherFactory) NewFetcher(_ logger.Logger, _ custmsg.MessageEmitter) compute.FetcherFn {
	return func(ctx context.Context, req *host.FetchRequest) (*host.FetchResponse, error) {
		return nil, fmt.Errorf("no fetcher configured")
	}
}

func Test_LoadProofOfReserve_FixedCronSchedule(t *testing.T) {
	t.Skip("This test should not be run as part of CI, it is for load testing only, comment/uncomment this line as required")

	resultsDir := getResultDir(t)

	numberOfNodes := 4
	f := uint8(1)
	protocolRoundTime := 50 * time.Millisecond
	numberOfWorkflows := 5
	getNextCronSchedule := func() string {
		return "*/30 * * * * *"
	}

	timeBetweenIncreasingWorkflowCount := 1 * time.Millisecond

	waitTimeAfterRegistrationComplete := 1 * time.Minute

	runLoadTest(t, numberOfNodes, f, numberOfWorkflows, protocolRoundTime, getNextCronSchedule, waitTimeAfterRegistrationComplete,
		timeBetweenIncreasingWorkflowCount, resultsDir)
}

func Test_LoadProofOfReserve_RoundRobinCronSchedule(t *testing.T) {
	t.Skip("This test should not be run as part of CI, it is for load testing only, comment/uncomment this line as required")

	resultsDir := getResultDir(t)

	numberOfNodes := 4
	f := uint8(1)
	protocolRoundTime := 50 * time.Millisecond
	numberOfWorkflows := 5

	count := 0
	getNextCronSchedule := func() string {
		nextSecond := count % 60
		count++
		return fmt.Sprintf("%d * * * * *", nextSecond)
	}

	timeBetweenIncreasingWorkflowCount := 1 * time.Millisecond

	waitTimeAfterRegistrationComplete := 2 * time.Minute

	runLoadTest(t, numberOfNodes, f, numberOfWorkflows, protocolRoundTime, getNextCronSchedule,
		waitTimeAfterRegistrationComplete, timeBetweenIncreasingWorkflowCount, resultsDir)
}

func getResultDir(t *testing.T) string {
	const resultDirEnvVar = "LOAD_TEST_RESULTS_DIR"
	resultsDir := os.Getenv(resultDirEnvVar)
	if resultsDir == "" {
		t.Fatalf("%s env var must be set to the directory to write the results to", resultDirEnvVar)
	}
	return resultsDir
}

func runLoadTest(t *testing.T, numberOfNodes int, f uint8, numberOfWorkflows int, protocolRoundTime time.Duration,
	getNextCronSchedule func() string, waitTimeAfterRegistrationComplete time.Duration, timeBetweenIncreasingWorkflowCount time.Duration,
	resultsDir string) {
	ctx := t.Context()

	lggr := logger.Test(t)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	targetSink, registerWorkflowOnDon := setupLoadtestDON(ctx, t, lggr, numberOfNodes, f, protocolRoundTime)

	resultsHeader := fmt.Sprintf("Load test with increasing workflows, time between increasing work flowcount %s, numberOfNodes %d, protocolRountTime %s, finalNumberOfWorkflows %d\n",
		timeBetweenIncreasingWorkflowCount, numberOfNodes, protocolRoundTime, numberOfWorkflows)

	sampler := newSampler(resultsHeader, numberOfNodes)

	workflowCountCh := make(chan int, 1000)

	go func() {
		workflowCount := 0
		for {
			select {
			case req := <-targetSink.Sink:
				err := sampler.AddSample(req, workflowCount)
				require.NoError(t, err)
			case cnt := <-workflowCountCh:
				workflowCount = cnt
			}
		}
	}()

	workflowName := "LoadWf"

	for numWorkflowsRegistered := range numberOfWorkflows {
		registerWorkflowOnDon(workflowName+strconv.Itoa(numWorkflowsRegistered), getNextCronSchedule(), numWorkflowsRegistered+1)

		workflowCountCh <- numWorkflowsRegistered + 1

		// want to do here is wait for each workflow to be started or just pause and wait?
		time.Sleep(timeBetweenIncreasingWorkflowCount)
	}

	// Now wait for all workflows to go live
	time.Sleep(waitTimeAfterRegistrationComplete)

	report := sampler.AggregatedByScheduledStartReport()

	filePath := generateFilePath(resultsDir)
	err := os.WriteFile(filePath, []byte(report), 0600)
	require.NoError(t, err)
	fmt.Printf("Report written to %s\n", filePath)

	fmt.Println(report)
}

func setupLoadtestDON(ctx context.Context, t *testing.T, lggr logger.Logger, numberOfNodes int, f uint8,
	protocolRoundTime time.Duration) (*framework.TargetSink, func(workflowName string, cronSchedule string, workflowNumber int)) {
	chainID := uint64(1337)
	network := "evm"
	readCapabilityConfig, err := CreateReadContractCapabilityConfig(chainID, network)
	require.NoError(t, err)

	config := readBalancesConfig{
		ChainID: strconv.FormatUint(chainID, 10),
	}

	urlToConfigBytes := map[string][]byte{}

	fetcherFunc := func(ctx context.Context, messageID string, req capabilities.Request) ([]byte, error) {
		url := req.URL
		if strings.HasPrefix(url, "workflows") {
			compressedBinary, err := os.ReadFile(url)
			require.NoError(t, err)
			base64EncodedCompressedBinary := base64.StdEncoding.EncodeToString(compressedBinary)
			return []byte(base64EncodedCompressedBinary), nil
		}

		if configBytes, ok := urlToConfigBytes[url]; ok {
			return configBytes, nil
		}

		return nil, fmt.Errorf("unknown url: %s", url)
	}

	donContext := framework.CreateDonContextWithWorkflowRegistry(ctx, t, fetcherFunc, NullFetcherFactory{})

	addresses := []string{
		"0x5c25312C82791e6cB76Dc9eFaBE2F5fa695D966b",
		"0xAc85bE3811e06712f53BC11844Ed8a37d3e9C3Ab",
	}
	config.Addresses = addresses

	err = fundAddress(ctx, t, donContext.EthBlockchain, addresses[0], 100)
	require.NoError(t, err)
	err = fundAddress(ctx, t, donContext.EthBlockchain, addresses[1], 50)
	require.NoError(t, err)

	balanceReaderAddr, _, _, err := contract.DeployBalanceReader(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)
	donContext.EthBlockchain.Commit()
	config.BalanceReaderContractAddress = balanceReaderAddr.String()

	cronBinary, err := utils.DeployCapability(t, "cron")
	require.NoError(t, err)

	readContractBinary, err := utils.DeployCapability(t, "readcontract")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: numberOfNodes, F: f, AcceptsWorkflows: true})
	require.NoError(t, err)

	triggerSink := framework.NewTriggerSink(t, "mock-trigger", "1.0.0")

	targetSink := framework.NewTargetSink("target-sink", "1.0.0")

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{},
		donContext, true, protocolRoundTime)

	workflowDon.AddExternalTriggerCapability(triggerSink)

	workflowDon.AddStandardCapability("cron-capabilities", cronBinary, utils.GetCronConfig(t, 1))
	computeConfig, err := toml.Marshal(defaultConfig)
	require.NoError(t, err)
	workflowDon.AddStandardCapability("compute-capability", commandOverrideForCustomComputeAction, "'''"+string(computeConfig)+"'''")
	workflowDon.AddOCR3NonStandardCapability()
	workflowDon.AddTargetCapability(targetSink)
	workflowDon.AddPublishedStandardCapability("readcontract-capability", readContractBinary, readCapabilityConfig,
		&pb.CapabilityConfig{}, kcr.CapabilitiesRegistryCapability{
			LabelledName:   fmt.Sprintf("read-contract-%s-%d", network, chainID),
			Version:        "1.0.0",
			CapabilityType: uint8(registrysyncer.ContractCapabilityTypeAction),
		})

	workflowDon.Initialise()

	config.WriteTargetCapabilityID = targetSink.GetTargetID()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	var registerWorkflowOnDon = func(workflowName string, cronSchedule string, workflowNumber int) {
		binaryURL := fmt.Sprintf("workflows/workflowwasmfiles_generated/workflow%d/workflow%d.brotli",
			workflowNumber, workflowNumber)
		compressedBinary, err := os.ReadFile(binaryURL)
		require.NoError(t, err)

		config.CronSchedule = cronSchedule

		secretsURL := ""

		configURL := workflowName + "-config.json"

		configBytes, err := json.Marshal(config)
		require.NoError(t, err)

		urlToConfigBytes[configURL] = configBytes

		registerWorkflow(t, donContext, workflowName, compressedBinary, secretsURL, workflowDon,
			binaryURL, configURL, configBytes)
	}
	return targetSink, registerWorkflowOnDon
}

func generateFilePath(baseName string) string {
	timestamp := time.Now().Format("20060102_150405")
	return fmt.Sprintf("%s_%s.csv", baseName, timestamp)
}

type sampler struct {
	outputHeader                 string
	samples                      []loadTestSample
	samplesByWorkflowExecutionID map[string][]loadTestSample
	samplesByScheduledStartTime  map[time.Time][]loadTestSample
	numNodes                     int
	activeWorkflowCount          int
	mu                           sync.Mutex

	uniqueWorkflowIDs map[string]bool
}

func newSampler(outputHeader string, numNodes int) *sampler {
	return &sampler{
		samplesByWorkflowExecutionID: make(map[string][]loadTestSample),
		samplesByScheduledStartTime:  make(map[time.Time][]loadTestSample),
		outputHeader:                 outputHeader,
		numNodes:                     numNodes,
		uniqueWorkflowIDs:            map[string]bool{},
	}
}

func (s *sampler) AddSample(req commoncap.CapabilityRequest, registeredWorkflowCount int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	endTime := time.Now()

	s.uniqueWorkflowIDs[req.Metadata.WorkflowID] = true

	var scheduledStartTimeStr string
	err := req.Inputs.Underlying["ScheduledStartTime"].UnwrapTo(&scheduledStartTimeStr)
	if err != nil {
		return fmt.Errorf("failed to unwrap scheduled start time: %v", err)
	}

	var startTimeStr string
	err = req.Inputs.Underlying["StartTime"].UnwrapTo(&startTimeStr)
	if err != nil {
		return fmt.Errorf("failed to unwrap start time: %v", err)
	}

	layout := time.RFC3339
	startTime, err := time.Parse(layout, startTimeStr)
	if err != nil {
		return fmt.Errorf("failed to parse start time: %v", err)
	}

	scheduledStartTime, err := time.Parse(layout, scheduledStartTimeStr)
	if err != nil {
		return fmt.Errorf("failed to parse scheduled start time: %v", err)
	}

	sample := newLoadTestSample(
		req.Metadata.WorkflowDonID,
		scheduledStartTime,
		startTime,
		endTime,
		req.Metadata.WorkflowID, registeredWorkflowCount, req.Metadata.WorkflowExecutionID,
		len(s.uniqueWorkflowIDs))

	s.samples = append(s.samples, sample)

	s.samplesByScheduledStartTime[scheduledStartTime] = append(s.samplesByScheduledStartTime[scheduledStartTime], sample)
	s.samplesByWorkflowExecutionID[req.Metadata.WorkflowExecutionID] = append(s.samplesByWorkflowExecutionID[req.Metadata.WorkflowExecutionID], sample)
	samplesForWorkflowExecutionID := s.samplesByWorkflowExecutionID[req.Metadata.WorkflowExecutionID]

	// Sanity check the data
	firstScheduledStartTime := samplesForWorkflowExecutionID[0].ScheduledStartTime
	for _, sample := range samplesForWorkflowExecutionID {
		if sample.ScheduledStartTime != firstScheduledStartTime {
			return fmt.Errorf("multiple samples for same workflow execution id with different scheduled start times")
		}
	}

	return nil
}

func (s *sampler) ActiveWorkflowCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeWorkflowCount
}

func (s *sampler) SetActiveWorkflowCount(count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeWorkflowCount = count
}

func (s *sampler) SampleCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.samples)
}

func (s *sampler) AggregatedByScheduledStartReport() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	report := s.outputHeader + "\n"

	// first calculate number of running workflows from the samples

	samplesByScheduledStartTime := make(map[time.Time][]loadTestSample)
	for _, sample := range s.samples {
		samplesByScheduledStartTime[sample.ScheduledStartTime] = append(samplesByScheduledStartTime[sample.ScheduledStartTime], sample)
	}

	var sortedStartTimes []time.Time
	for key := range samplesByScheduledStartTime {
		sortedStartTimes = append(sortedStartTimes, key)
	}

	sort.Slice(sortedStartTimes, func(i, j int) bool {
		return sortedStartTimes[i].Before(sortedStartTimes[j])
	})

	report += "ScheduledStartTime, Average Latency (ms), Number of Workflows Running, Average Actual Start Time, Num Workflows Registered\n"

	for _, scheduledStartTime := range sortedStartTimes {
		samples := samplesByScheduledStartTime[scheduledStartTime]

		latencySum := time.Duration(0)
		var averageActualStartTimeSum int64
		for _, sample := range samples {
			latencySum += sample.SampleTime
			averageActualStartTimeSum += sample.StartTime.UnixMilli()
		}

		averageLatencyMs := latencySum.Milliseconds() / int64(len(samples))
		averageActualStartTimeMs := averageActualStartTimeSum / int64(len(samples))
		averageActualStartTime := time.UnixMilli(averageActualStartTimeMs)

		report += fmt.Sprintf("%s, %d, %d, %s, %d\n", scheduledStartTime.Format(time.RFC3339), averageLatencyMs, samples[0].RunningWorkflowsCount, averageActualStartTime.Format(time.RFC3339), samples[0].RegisteredWorkflowCount)
	}

	return report
}

func (s *sampler) RawReport() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	str := s.outputHeader + "\n"
	str += s.samples[0].ColumnTitle() + "\n"
	for _, sample := range s.samples {
		str += sample.String() + "\n"
	}

	return str
}

type loadTestSample struct {
	DonID                   uint32
	RegisteredWorkflowCount int
	ScheduledStartTime      time.Time
	StartTime               time.Time
	EndTime                 time.Time
	SampleTime              time.Duration
	WorkflowID              string
	WorkflowExecutionID     string
	RunningWorkflowsCount   int
}

func newLoadTestSample(donID uint32, scheduledStartTime, startTime, endTime time.Time, workflowID string, registeredWorkflowCount int,
	workflowExecutionID string, runningWorkflowsCount int) loadTestSample {
	return loadTestSample{
		DonID:                   donID,
		RegisteredWorkflowCount: registeredWorkflowCount,
		ScheduledStartTime:      scheduledStartTime,
		StartTime:               startTime,
		EndTime:                 endTime,
		WorkflowID:              workflowID,
		SampleTime:              endTime.Sub(startTime),
		WorkflowExecutionID:     workflowExecutionID,
		RunningWorkflowsCount:   runningWorkflowsCount,
	}
}

func (s *loadTestSample) ColumnTitle() string {
	return "DonID, WorkflowID, StartTime, EndTime, Latency (ms), ScheduledStartTime, StartedWorkflows, Running Workflows"
}

func (s *loadTestSample) String() string {
	startTimeStr := s.StartTime.Format(time.RFC3339)
	endTimeStr := s.EndTime.Format(time.RFC3339)
	scheduledStartTimeStr := s.ScheduledStartTime.Format(time.RFC3339)

	return fmt.Sprintf("%d,%s,%s,%s,%d,%s,%d,%d",
		s.DonID, s.WorkflowID, startTimeStr, endTimeStr, s.SampleTime.Milliseconds(), scheduledStartTimeStr,
		s.RegisteredWorkflowCount, s.RunningWorkflowsCount)
}

func fundAddress(ctx context.Context, t *testing.T, ethBlockChain *framework.EthBlockchain, address1Str string, amount int) error {
	address1 := common2.HexToAddress(address1Str)
	n, err := ethBlockChain.Client().NonceAt(ctx, ethBlockChain.TransactionOpts().From, nil)
	require.NoError(t, err)

	tx := testutils.NewLegacyTransaction(n, address1, assets.Ether(amount).ToInt(), 21000, big.NewInt(1000000000), nil)
	signedTx, err := ethBlockChain.TransactionOpts().Signer(ethBlockChain.TransactionOpts().From, tx)
	require.NoError(t, err)
	err = ethBlockChain.Client().SendTransaction(ctx, signedTx)
	require.NoError(t, err)
	ethBlockChain.Commit()
	return err
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

func CreateReadContractCapabilityConfig(chainID uint64, network string) (string, error) {
	readContractConfig := ReadContractConfig{
		ChainID: chainID,
		Network: network,
	}

	configJSON, err := json.Marshal(readContractConfig)
	if err != nil {
		return "", fmt.Errorf("failed to marshal read contract config: %v", err)
	}

	readCapabilityConfig := "'''" + string(configJSON) + "'''"
	return readCapabilityConfig, nil
}
