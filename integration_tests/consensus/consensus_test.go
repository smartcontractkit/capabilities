package consensus

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder/beholdertest"
	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	events2 "github.com/smartcontractkit/chainlink-protos/workflows/go/events"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/capabilities"

	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

func Test_Consensus(t *testing.T) {
	t.Skip("PRODCRE-834 Needs to be fixed")
	ctx := t.Context()
	beholderTester := beholdertest.NewObserver(t)

	lggr := logger.TestLogger(t)
	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	readBalancesWithConfigPath, err := filepath.Abs("./workflow")
	require.NoError(t, err)

	wasmFile := filepath.Join(readBalancesWithConfigPath, "consensus.wasm")
	mainFile := filepath.Join(readBalancesWithConfigPath, "main_wasip1.go")
	utils.CreateWasmBinary(t, mainFile, wasmFile)

	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	numOfWorkflowNodes := 4
	workflowName := "a1b2c3d4e5f6a1b2c3d4"

	setupDon(ctx, t, lggr, wasmFile, targetSink, numOfWorkflowNodes, workflowName)

	triggerTimeIdenticalConsensusValue := map[string][]string{}
	require.Eventually(t, func() bool {
		workflowLogs := getBeholderLogsForWorkflow(beholderTester, t, workflowName)

		// Wait until we have the logs for all workflows
		if len(workflowLogs) < numOfWorkflowNodes {
			return false
		}

		for _, logs := range workflowLogs {
			// Expect only one log line
			require.Len(t, logs, 1, "Expected exactly one log line per workflow")

			log := logs[0]

			if strings.Contains(log.Message, "V2 Workflow Execution Result") {
				re := regexp.MustCompile(`trigger time seconds:(\d+)`)
				matches := re.FindStringSubmatch(log.Message)
				triggerTime := matches[1]
				re = regexp.MustCompile(`consensus value (\d+)`)
				matches = re.FindStringSubmatch(log.Message)
				consensusValue := matches[1]
				triggerTimeIdenticalConsensusValue[triggerTime] = append(triggerTimeIdenticalConsensusValue[triggerTime], consensusValue)
			}
		}

		// Check if we have expected number of identical consensus values for each trigger time
		for _, consensusValues := range triggerTimeIdenticalConsensusValue {
			if len(consensusValues) == numOfWorkflowNodes {
				if len(consensusValues) == numOfWorkflowNodes && allEqual(consensusValues) {
					return true
				}
			}
		}

		return false
	}, 90*time.Second, 100*time.Millisecond)
}

func allEqual(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] != values[0] {
			return false
		}
	}
	return true
}

func setupDon(ctx context.Context, t *testing.T, lggr logger.Logger, workflowURL string, targetSink framework.TargetFactory,
	numOfWorkflowNodes int, workflowName string) {
	configURL := "workflow-config.json"
	compressedBinary, base64EncodedCompressedBinary := utils.GetCompressedWorkflowWasm(t, workflowURL)

	syncerFetcherFunc := func(ctx context.Context, messageID string, req capabilities.Request) ([]byte, error) {
		url := req.URL
		switch url {
		case workflowURL:
			return []byte(base64EncodedCompressedBinary), nil
		case configURL:
			return []byte(""), nil
		}

		return nil, fmt.Errorf("unknown  url: %s", url)
	}

	donContext := framework.CreateDonContextWithWorkflowRegistry(ctx, t, syncerFetcherFunc, utils.NoopComputeFetcherFactory{})

	cronBinary, err := utils.DeployCapability(t, "cron")
	require.NoError(t, err)
	consensusBinary, err := utils.DeployCapability(t, "consensus")

	require.NoError(t, err)

	// Setup workflow DON
	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "ConsensusWorkflow", NumNodes: numOfWorkflowNodes, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	workflowDon.AddStandardCapability("cron-capabilities", cronBinary, utils.GetCronConfig(t, 1))
	workflowDon.AddStandardCapability("consensus-capabilities", consensusBinary, GetConsensusConfig(t, 10000))

	workflowDon.AddOCR3NonStandardCapability()
	workflowDon.AddTargetCapability(targetSink)

	workflowDon.Initialise()

	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, workflowDon)

	registerWorkflow(t, donContext, workflowName, compressedBinary, "", workflowDon,
		workflowURL, configURL, []byte{})
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

func getBeholderLogsForWorkflow(beholderTester beholdertest.Observer, t *testing.T, workflowName string) [][]*events2.LogLine {
	var workflowLogs [][]*events2.LogLine

	userMsgs := beholderTester.Messages(t, "beholder_data_schema", "/cre-events-user-logs/v1")
	if len(userMsgs) > 0 {
		for _, userMsg := range userMsgs {
			userLog := events2.UserLogs{}
			err := proto.Unmarshal(userMsg.Body, &userLog)
			require.NoError(t, err)
			workflowLogs = append(workflowLogs, userLog.LogLines)
		}
	}

	return workflowLogs
}

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
