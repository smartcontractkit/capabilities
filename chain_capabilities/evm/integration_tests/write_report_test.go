package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/actions/mocks"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/config"

	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/gethwrappers"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/monitoring"
	"github.com/smartcontractkit/capabilities/chain_capabilities/evm/test"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	evmcappb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/evm"
	"github.com/smartcontractkit/chainlink-common/pkg/loop"
	commontypes "github.com/smartcontractkit/chainlink-common/pkg/types"
	"github.com/smartcontractkit/chainlink-evm/pkg/assets"
	"github.com/smartcontractkit/chainlink-evm/pkg/types"
	"github.com/smartcontractkit/chainlink/v2/core/logger"
	"github.com/smartcontractkit/chainlink/v2/core/services/chainlink"
	"github.com/smartcontractkit/chainlink/v2/core/utils/testutils/evm"
	"github.com/stretchr/testify/require"
)

func Test_WriteReport(t *testing.T) {
	ctx := t.Context()
	receiverAddress := common.HexToAddress("0x6D7C976AE064a519bCB37C4361C8990fEA425fA7")
	var keystoneForwaderContractAddress common.Address
	var kfc *gethwrappers.KeystoneForwarderTest

	testApp := evm.NewTestAppWithSimulatedBackend(t, evm.TestAppConfig{
		CustomizeConfigFunc: func(config *chainlink.GeneralConfig, backend evm.SimulatedBackend, transactOptsAccoutns evm.TransactOptsAccounts) {
			keystoneForwaderContractAddress = deployAndConfigureKeystoneForwader(t, kfc, transactOptsAccoutns, backend)
			applyEVMRelayerConfig(config, transactOptsAccoutns)
		},
	})
	app := testApp.App
	require.NoError(t, app.Start(t.Context()))
	defer testApp.App.Stop()

	simulatedBackend := testApp.SimulatedBackend
	relayers := app.GetRelayers()
	evmRelayer, err := relayers.Get(commontypes.NewRelayID(simulatedBackend.Network, simulatedBackend.ChainID.String()))
	require.NoError(t, err)

	evm := createEVMService(t, evmRelayer, simulatedBackend, keystoneForwaderContractAddress)
	requestMetadata, writeReportRequest := createWriteReportRequest(t, receiverAddress)

	reply, err := evm.WriteReport(ctx, requestMetadata, writeReportRequest)
	assertWriteReportReply(t, err, reply, receiverAddress)

	reply, err = evm.WriteReport(ctx, requestMetadata, writeReportRequest)
	assertWriteReportReply(t, err, reply, receiverAddress)

	totalTxs := getTotalNumberOfTXs(ctx, t, testApp.SimulatedBackend)
	require.Equal(t, 3, totalTxs)
}

func getTotalNumberOfTXs(ctx context.Context, t *testing.T, simulatedBackend evm.SimulatedBackend) int {
	client := simulatedBackend.Backend.Client()
	header, err := client.HeaderByNumber(ctx, nil)
	require.NoError(t, err)

	latestBlock := int(header.Number.Int64())
	var totalTxs int = 0

	for i := int(0); i <= latestBlock; i++ {
		block, err := client.BlockByNumber(ctx, big.NewInt(int64(i)))
		if err != nil {
			continue
		}
		totalTxs += len(block.Transactions())
	}
	return totalTxs
}

func assertWriteReportReply(t *testing.T, err error, reply *evmcappb.WriteReportReply, receiverAddress common.Address) {
	require.NoError(t, err)
	require.NotEmpty(t, reply.TxHash)
	require.NotEmpty(t, reply.ErrorMessage)
	require.Contains(t, *reply.ErrorMessage, fmt.Sprintf("Invalid receiver: %s", strings.ToLower(receiverAddress.Hex()[2:])))
	require.Equal(t, reply.TxStatus, evmcappb.TxStatus_TX_STATUS_SUCCESS)
	require.Equal(t, *reply.ReceiverContractExecutionStatus, evmcappb.ReceiverContractExecutionStatus_RECEIVER_CONTRACT_EXECUTION_STATUS_REVERTED)
}

func applyEVMRelayerConfig(config *chainlink.GeneralConfig, transactOptsAccoutns evm.TransactOptsAccounts) {
	c := *config
	fromAddress := types.EIP55AddressFromAddress(transactOptsAccoutns.TransactOpts[1].From)
	c.EVMConfigs()[0].Workflow.FromAddress = &fromAddress
	c.EVMConfigs()[0].GasEstimator.PriceMax = assets.NewWei(big.NewInt(50000000))
}

func deployAndConfigureKeystoneForwader(t *testing.T, kfc *gethwrappers.KeystoneForwarderTest, transactOptsAccoutns evm.TransactOptsAccounts, backend evm.SimulatedBackend) common.Address {
	var err error
	keystoneForwaderContractAddress, _, kfc, err := gethwrappers.DeployKeystoneForwarderTest(transactOptsAccoutns.TransactOpts[0], backend.Backend.Client())
	require.NoError(t, err)
	backend.Backend.Commit()

	_, err = kfc.AddForwarder(transactOptsAccoutns.Deployer, keystoneForwaderContractAddress)
	require.NoError(t, err)
	backend.Backend.Commit()
	return keystoneForwaderContractAddress
}

func createWriteReportRequest(t *testing.T, receiverAddress common.Address) (capabilities.RequestMetadata, *evmcappb.WriteReportRequest) {
	report, err := NewTestReport(t, []byte{})
	require.NoError(t, err)
	reportCtx := []byte{42}
	sigs := [][]byte{{1, 2, 3}}

	repDecoded, err := Decode(report)
	require.NoError(t, err)

	requestMetadata := capabilities.RequestMetadata{
		WorkflowID:               repDecoded.WorkflowID,
		WorkflowOwner:            repDecoded.WorkflowOwner,
		WorkflowExecutionID:      repDecoded.ExecutionID,
		WorkflowDonConfigVersion: repDecoded.DONConfigVersion,
		WorkflowName:             repDecoded.WorkflowName,
	}

	reportID, err := hex.DecodeString(repDecoded.ReportID)
	require.NoError(t, err)
	writeReportRequest := evmcappb.WriteReportRequest{
		Receiver: receiverAddress[:],
		Report: &evmcappb.SignedReport{
			RawReport:     report,
			ReportContext: reportCtx,
			Signatures:    sigs,
			Id:            reportID,
		},
		GasConfig: &evmcappb.GasConfig{GasLimit: 10000000000},
	}
	return requestMetadata, &writeReportRequest
}

func createEVMService(t *testing.T, evmRelayer loop.Relayer, simulatedBackend evm.SimulatedBackend, keystoneForwaderContractAddress common.Address) actions.EVM {
	config := config.Config{
		ChainID:                simulatedBackend.ChainID.Uint64(),
		Network:                simulatedBackend.Network,
		CREForwarderAddress:    keystoneForwaderContractAddress.Hex(),
		BlockDepth:             100,
		ReceiverGasMinimum:     2000000,
		LogTriggerPollInterval: time.Hour,
	}
	evmService, err := evmRelayer.EVM()
	require.NoError(t, err)
	consensusHandler := mocks.NewConsensusHandler(t)
	evm, err := actions.NewEVM(config, evmService, logger.TestLogger(t), test.NopBeholderProcessor{}, &monitoring.MessageBuilder{}, consensusHandler)
	require.NoError(t, err)
	return evm
}

func RandomHex(n int) string {
	byteLen := (n + 1) / 2 // round up if n is odd
	bytes := make([]byte, byteLen)
	_, _ = rand.Read(bytes)
	hexStr := hex.EncodeToString(bytes)
	return hexStr[:n]
}
