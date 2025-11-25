package readcontract

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	beholderpb "github.com/smartcontractkit/chainlink-common/pkg/beholder/pb"
	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/utils/tests"
	kcr "github.com/smartcontractkit/chainlink-evm/gethwrappers/keystone/generated/capabilities_registry_1_1_0"
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/registrysyncer"

	"github.com/smartcontractkit/capabilities/integration_tests/readcontract/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

type ReadContractConfig struct {
	ChainID uint64 `json:"chainId"`
	Network string `json:"network"`
}

func Test_RemoteReadCapabilityWithoutConsensus(t *testing.T) {
	t.Skip("no longer supported")
	ctx := t.Context()
	lggr := logger.TestLogger(t)

	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	targetSink := readValueFromContractFunction(ctx, t, lggr, "GetValue", 4)

	// Use a timeout mechanism to avoid indefinite blocking
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	select {
	case readresult := <-targetSink.Sink:
		require.NotNil(t, readresult)
		require.Equal(t, CreateExpectedValue(t, []int64{21, 42, 63}), readresult.Inputs)
	case <-ctxWithTimeout.Done():
		t.Fatal("timeout waiting for read result from target sink")
	}
}

func Test_RemoteReadCapabilityMisconfiguredContractError(t *testing.T) {
	t.Skip("no longer supported")
	beholderTester := tests.Beholder(t) //nolint:staticcheck

	ctx := t.Context()
	lggr := logger.TestLogger(t)

	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	numOfWorkflowNodes := 4
	readValueFromContractFunction(ctx, t, lggr, "GetValue2", numOfWorkflowNodes)
	require.Eventually(t, func() bool {
		beholderLogs := getBeholderLogsForStep(beholderTester, t, "abcdef0123", "action2")

		var readContractCapabilityErrorMessages []*beholderpb.BaseMessage
		for _, log := range beholderLogs {
			if strings.Contains(log.Msg, "method GetValue2 doesn't exist") {
				readContractCapabilityErrorMessages = append(readContractCapabilityErrorMessages, log)
			}
		}

		return len(readContractCapabilityErrorMessages) == numOfWorkflowNodes
	}, 10*time.Second, 100*time.Millisecond)
}

func readValueFromContractFunction(ctx context.Context, t *testing.T, lggr logger.Logger, contractFunc string,
	numOfWorkflowNodes int) *framework.TargetSink {
	donContext := framework.CreateDonContext(ctx, t)

	address, _, _, err := contract.DeployContract(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)

	readContractBinary, err := utils.DeployCapability(t, "readcontract")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: numOfWorkflowNodes, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	readCapabilityDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "ReadCapability", NumNodes: 4, F: 1, AcceptsWorkflows: false})
	require.NoError(t, err)

	triggerSink := framework.NewTriggerSink(t, "mock-trigger", "1.0.0")
	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{readCapabilityDonConfiguration.DON},
		donContext, true, 1*time.Second)

	readCapabilityDon := framework.NewDON(ctx, t, lggr, readCapabilityDonConfiguration,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	// Note: it is expected that the workflow don always has an  at least one external capability, failure to do so will
	// cause adding a node to the capability registry contract to fail - arguably a bug in the contract
	workflowDon.AddExternalTriggerCapability(triggerSink)
	workflowDon.AddTargetCapability(targetSink)

	chainID := uint64(1337)
	network := "evm"
	readCapabilityConfig, err := CreateReadContractCapabilityConfig(chainID, network)
	require.NoError(t, err)

	readCapabilityDon.AddPublishedStandardCapability("readcontract-capability", readContractBinary, readCapabilityConfig,
		&pb.CapabilityConfig{}, kcr.CapabilitiesRegistryCapability{
			LabelledName:   fmt.Sprintf("read-contract-%s-%d", network, chainID),
			Version:        "1.0.0",
			CapabilityType: uint8(registrysyncer.ContractCapabilityTypeAction),
		})

	workflowDon.Initialise()
	readCapabilityDon.Initialise()
	servicetest.Run(t, readCapabilityDon)
	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, readCapabilityDon, workflowDon)

	workflowJob := CreateWorkflowJobForTest(t, workflowName, workflowOwnerID, network, strconv.FormatUint(chainID, 10),
		address.String(), "ValueSource", contractFunc, contract.ContractMetaData.ABI)

	err = workflowDon.AddJob(ctx, &workflowJob)
	require.NoError(t, err)

	contractReadActionParams, err := values.WrapMap(map[string]any{
		"ConfidenceLevel": "unconfirmed",
		"Params":          map[string]any{},
	})
	require.NoError(t, err)

	triggerSink.SendOutput(contractReadActionParams, uuid.New().String())
	return targetSink
}

func getBeholderLogsForStep(beholderTester tests.BeholderTester, t *testing.T, workflowName string, stepRef string) []*beholderpb.BaseMessage { //nolint:staticcheck
	baseMessages, err := beholderTester.BaseMessagesForLabels(t, map[string]string{
		"workflowName": workflowName,
		"stepRef":      stepRef,
	})
	require.NoError(t, err)
	return baseMessages
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

func CreateExpectedValue(t *testing.T, expectedValues []int64) values.Value {
	arrayValues, err := values.NewList(func() []any {
		vals := make([]any, len(expectedValues))
		for i, v := range expectedValues {
			vals[i] = values.NewBigInt(big.NewInt(v))
		}
		return vals
	}())
	require.NoError(t, err)

	expectedValue, err := values.WrapMap(map[string]values.Value{
		"LatestValue": arrayValues,
	})
	require.NoError(t, err)

	return expectedValue
}
