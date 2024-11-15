package readcontract

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/gethwrappers/keystone/generated/capabilities_registry"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/registrysyncer"

	"github.com/smartcontractkit/capabilities/integration_tests/readcontract/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

type ReadContractConfig struct {
	ChainID           uint64 `json:"chainId"`
	Network           string `json:"network"`
	SupportsConsensus bool   `json:"supportsConsensus"`
}

func Test_RemoteReadCapabilityWithConsensus(t *testing.T) {
	testRemoteReadContractCapability(t, true, "15s")
}

func Test_RemoteReadCapabilityWithoutConsensus(t *testing.T) {
	testRemoteReadContractCapability(t, false, "")
}

func testRemoteReadContractCapability(t *testing.T, withConsensus bool, pollingInterval string) {
	ctx, cancel := framework.Context(t)
	defer cancel()

	lggr := logger.TestLogger(t)
	lggr.SetLogLevel(zapcore.InfoLevel)

	defer func() {
		utils.CleanupCapabilities(lggr)
	}()

	donContext := framework.CreateDonContext(ctx, t)

	address, _, _, err := contract.DeployContract(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)

	readContractBinary, err := utils.DeployCapability(t, "readcontract")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: 7, F: 2, AcceptsWorkflows: true})
	require.NoError(t, err)

	readCapabilityDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "ReadCapability", NumNodes: 7, F: 2, AcceptsWorkflows: false})
	require.NoError(t, err)

	triggerSink := framework.NewTriggerSink(t, "mock-trigger", "1.0.0")
	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{readCapabilityDonConfiguration.DON},
		donContext, true)

	readCapabilityDon := framework.NewDON(ctx, t, lggr, readCapabilityDonConfiguration,
		[]commoncap.DON{},
		donContext, true)

	// Note: it is expected that the workflow don always has an  at least one external capability, failure to do so will
	// cause adding a node to the capability registry contract to fail - arguably a bug in the contract
	workflowDon.AddExternalTriggerCapability(triggerSink)
	workflowDon.AddTargetCapability(targetSink)

	chainID := uint64(1337)
	network := "evm"
	readCapabilityConfig, err := CreateReadContractCapabilityConfig(chainID, network, withConsensus)
	require.NoError(t, err)

	readCapabilityDon.AddPublishedStandardCapability("readcontract-capability", readContractBinary, readCapabilityConfig,
		&pb.CapabilityConfig{}, capabilities_registry.CapabilitiesRegistryCapability{
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
		address.String(), "ValueSource", "GetValue", contract.ContractMetaData.ABI,
		withConsensus, pollingInterval, 10)

	err = workflowDon.AddJob(ctx, &workflowJob)
	require.NoError(t, err)

	contractReadActionParams, err := values.WrapMap(map[string]any{
		"ConfidenceLevel": "unconfirmed",
		"Params":          map[string]any{},
	})
	require.NoError(t, err)

	triggerSink.SendOutput(contractReadActionParams)

	readresult := <-targetSink.Sink
	require.NotNil(t, readresult)
	require.Equal(t, CreateExpectedValue(t, []int64{21, 42, 63}), readresult.Inputs)
}

func Test_LocalReadCapabilityWithConsensus(t *testing.T) {
	testLocalReadContractCapability(t, true, "1s")
}

func Test_LocalReadCapabilityWithoutConsensus(t *testing.T) {
	testLocalReadContractCapability(t, false, "")
}

func testLocalReadContractCapability(t *testing.T, withConsensus bool, pollingInterval string) {
	ctx, cancel := framework.Context(t)
	defer cancel()
	lggr := logger.TestLogger(t)
	lggr.SetLogLevel(zapcore.InfoLevel)

	defer func() {
		utils.CleanupCapabilities(lggr)
	}()

	donContext := framework.CreateDonContext(ctx, t)

	address, _, _, err := contract.DeployContract(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)

	readContractBinary, err := utils.DeployCapability(t, "readcontract")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "Workflow", NumNodes: 7, F: 2, AcceptsWorkflows: true})
	require.NoError(t, err)

	triggerSink := framework.NewTriggerSink(t, "mock-trigger", "1.0.0")
	targetSink := framework.NewTargetSink("mock-target", "1.0.0")

	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{},
		donContext, true)

	// Note: it is expected that the workflow don always has an  at least one external capability, falure to do so will
	// cause adding a node to the capability registry contract to fail - arguably a bug in the contract
	workflowDon.AddExternalTriggerCapability(triggerSink)
	workflowDon.AddTargetCapability(targetSink)

	chainID := uint64(1337)
	network := "evm"
	readCapabilityConfig, err := CreateReadContractCapabilityConfig(chainID, network, withConsensus)
	require.NoError(t, err)

	workflowDon.AddStandardCapability("readcontract-capability", readContractBinary, readCapabilityConfig)

	servicetest.Run(t, workflowDon)

	workflowJob := CreateWorkflowJobForTest(t, workflowName, workflowOwnerID, network, strconv.FormatUint(chainID, 10),
		address.String(), "ValueSource", "GetValue", contract.ContractMetaData.ABI,
		withConsensus, pollingInterval, 10)

	err = workflowDon.AddJob(ctx, &workflowJob)
	require.NoError(t, err)

	contractReadActionParams, err := values.WrapMap(map[string]any{
		"ConfidenceLevel": "unconfirmed",
		"Params":          map[string]any{},
	})
	require.NoError(t, err)

	triggerSink.SendOutput(contractReadActionParams)

	readresult := <-targetSink.Sink
	require.NotNil(t, readresult)
	require.Equal(t, CreateExpectedValue(t, []int64{21, 42, 63}), readresult.Inputs)
}

func CreateReadContractCapabilityConfig(chainID uint64, network string, supportsConsensus bool) (string, error) {
	readContractConfig := ReadContractConfig{
		ChainID:           chainID,
		Network:           network,
		SupportsConsensus: supportsConsensus,
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
