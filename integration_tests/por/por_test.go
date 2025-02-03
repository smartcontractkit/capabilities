package por

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	common2 "github.com/ethereum/go-ethereum/common"
	"github.com/pelletier/go-toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"

	commoncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/services/servicetest"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/compute"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/framework"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/integration_tests/keystone"
	"github.com/smartcontractkit/chainlink/v2/core/capabilities/webapi"
	kcr "github.com/smartcontractkit/chainlink/v2/core/gethwrappers/keystone/generated/capabilities_registry_1_1_0"
	feeds_consumer "github.com/smartcontractkit/chainlink/v2/core/gethwrappers/keystone/generated/feeds_consumer_1_0_0"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/gateway/handlers/common"
	"github.com/smartcontractkit/chainlink/v2/core/services/registrysyncer"
	"github.com/smartcontractkit/chainlink/v2/evm/assets"
	"github.com/smartcontractkit/chainlink/v2/evm/testutils"

	"github.com/smartcontractkit/capabilities/integration_tests/por/contract"
	"github.com/smartcontractkit/capabilities/integration_tests/utils"
)

const commandOverrideForCustomComputeAction = "__builtin_custom-compute-action"

var defaultConfig = compute.Config{
	ServiceConfig: webapi.ServiceConfig{
		RateLimiter: common.RateLimiterConfig{
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

func Test_PORReadbalances(t *testing.T) {
	ctx, cancel := framework.Context(t)
	defer cancel()

	lggr := logger.TestLogger(t)
	lggr.SetLogLevel(zapcore.InfoLevel)

	defer func() {
		utils.CleanupCapabilitiesDir(lggr)
	}()

	readBalancesWithConfigPath, err := filepath.Abs("../../workflows/readbalances-with-config/cmd")
	require.NoError(t, err)

	wasmFile := filepath.Join(readBalancesWithConfigPath, "readbalances.wasm")
	mainFile := filepath.Join(readBalancesWithConfigPath, "main.go")

	utils.CreateWasmBinary(t, mainFile, wasmFile)

	consumerContract := setupDons(ctx, t, lggr, wasmFile)

	feedsReceived := make(chan *feeds_consumer.KeystoneFeedsConsumerFeedReceived, 1000)
	feedsSub, err := consumerContract.WatchFeedReceived(&bind.WatchOpts{}, feedsReceived, nil)
	require.NoError(t, err)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

loop:
	for {
		select {
		case <-ctxWithTimeout.Done():
			t.Fatalf("timed out waiting for feed")
		case err := <-feedsSub.Err():
			require.NoError(t, err)
		case feed := <-feedsReceived:
			expectedResult := &big.Int{}
			expectedResult.SetString("150000000000000000000", 10)
			assert.Equal(t, feed.Price, expectedResult)
			fmt.Printf("FEED: %v\n", feed)
			break loop
		}
	}
}

func setupDons(ctx context.Context, t *testing.T, lggr logger.SugaredLogger, workflowURL string) *feeds_consumer.KeystoneFeedsConsumer {
	chainID := uint64(1337)
	network := "evm"
	readCapabilityConfig, err := CreateReadContractCapabilityConfig(chainID, network)
	require.NoError(t, err)

	configURL := "workflow-config.json"
	workflowConfig := readBalancesConfig{
		ChainID:      strconv.FormatUint(chainID, 10),
		CronSchedule: "* * * * * *",
	}

	compressedBinary, base64EncodedCompressedBinary := utils.GetCompressedWorkflowWasm(t, workflowURL)

	syncerFetcherFunc := func(ctx context.Context, url string, maxBytes uint32) ([]byte, error) {
		switch url {
		case workflowURL:
			return []byte(base64EncodedCompressedBinary), nil
		case configURL:
			configBytes, err := json.Marshal(workflowConfig)
			require.NoError(t, err)
			return configBytes, nil
		}

		return nil, fmt.Errorf("unknown  url: %s", url)
	}

	donContext := framework.CreateDonContextWithWorkflowRegistry(ctx, t, syncerFetcherFunc, utils.NoopComputeFetcherFactory{})

	workflowConfig.Addresses = fundAddresses(ctx, t, donContext.EthBlockchain, 100, 50)

	balanceReaderAddr, _, _, err := contract.DeployBalanceReader(donContext.EthBlockchain.TransactionOpts(), donContext.EthBlockchain.Client())
	require.NoError(t, err)
	donContext.EthBlockchain.Commit()
	workflowConfig.BalanceReaderContractAddress = balanceReaderAddr.String()

	cronBinary, err := utils.DeployCapability(t, "cron")

	require.NoError(t, err)

	readContractBinary, err := utils.DeployCapability(t, "readcontract")
	require.NoError(t, err)

	workflowDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "PorWorkflow", NumNodes: 4, F: 1, AcceptsWorkflows: true})
	require.NoError(t, err)

	readCapabilityDonConfiguration, err := framework.NewDonConfiguration(framework.NewDonConfigurationParams{Name: "PorReadCapability", NumNodes: 4, F: 1, AcceptsWorkflows: false})
	require.NoError(t, err)

	// Setup DONs
	workflowDon := framework.NewDON(ctx, t, lggr, workflowDonConfiguration,
		[]commoncap.DON{readCapabilityDonConfiguration.DON},
		donContext, true, 1*time.Second)

	writeCapabilityDon := framework.NewDON(ctx, t, lggr, readCapabilityDonConfiguration,
		[]commoncap.DON{},
		donContext, true, 1*time.Second)

	// Setup workflow DON
	workflowDon.AddStandardCapability("cron-capabilities", cronBinary, utils.GetCronConfig(t, 1))
	computeConfig, err := toml.Marshal(defaultConfig)
	require.NoError(t, err)
	workflowDon.AddStandardCapability("compute-capability", commandOverrideForCustomComputeAction, "'''"+string(computeConfig)+"'''")
	workflowDon.AddOCR3NonStandardCapability()
	workflowDon.AddPublishedStandardCapability("readcontract-capability", readContractBinary, readCapabilityConfig,
		&pb.CapabilityConfig{}, kcr.CapabilitiesRegistryCapability{
			LabelledName:   fmt.Sprintf("read-contract-%s-%d", network, chainID),
			Version:        "1.0.0",
			CapabilityType: uint8(registrysyncer.ContractCapabilityTypeAction),
		})

	workflowDon.Initialise()

	forwarderAddr, _ := keystone.SetupForwarderContract(t, workflowDon, donContext.EthBlockchain)

	workflowName := "TestWf"
	workflowOwner := donContext.EthBlockchain.TransactionOpts().From.String()
	consumerAddr, consumerContract := keystone.SetupConsumerContract(t, donContext.EthBlockchain, forwarderAddr, workflowOwner,
		workflows.HashTruncateName(workflowName))

	workflowConfig.ConsumerAddress = consumerAddr.String()

	// Setup Write capability DON
	writeTargetCapabilityID, err := writeCapabilityDon.AddPublishedEthereumWriteTargetNonStandardCapability(forwarderAddr)
	require.NoError(t, err)
	workflowConfig.WriteTargetCapabilityID = writeTargetCapabilityID

	writeCapabilityDon.Initialise()
	servicetest.Run(t, writeCapabilityDon)
	servicetest.Run(t, workflowDon)

	donContext.WaitForCapabilitiesToBeExposed(t, writeCapabilityDon, workflowDon)

	workflowConfigBytes, err := json.Marshal(workflowConfig)
	require.NoError(t, err)

	registerWorkflow(t, donContext, workflowName, compressedBinary, "", workflowDon,
		workflowURL, configURL, workflowConfigBytes)
	return consumerContract
}

func fundAddresses(ctx context.Context, t *testing.T, blockChain *framework.EthBlockchain, amount1, amount2 int) []string {
	addresses := []string{
		"0x5c25312C82791e6cB76Dc9eFaBE2F5fa695D966b",
		"0xAc85bE3811e06712f53BC11844Ed8a37d3e9C3Ab",
	}

	err := fundAddress(ctx, t, blockChain, addresses[0], amount1)
	require.NoError(t, err)
	err = fundAddress(ctx, t, blockChain, addresses[1], amount2)
	require.NoError(t, err)
	return addresses
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
