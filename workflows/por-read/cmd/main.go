package main

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm"

	"github.com/smartcontractkit/capabilities/cron/croncap"
	"github.com/smartcontractkit/capabilities/readcontract/readcontractcap"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/aggregators"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/targets/chainwriter"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
)

type computeOutput struct {
	Price int
	// TODO: specify decimals; requires a different consumer contract.
	// Decimal int
	FeedID    [32]byte
	Timestamp time.Time
}

type computeConfig struct {
	FeedID string
}

func convertFeedIDtoBytes(feedIDStr string) ([32]byte, error) {
	b, err := hex.DecodeString(feedIDStr[2:])
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(b), nil
}

func BuildWorkflow(config []byte) *sdk.WorkflowSpecFactory {
	workflow := sdk.NewWorkflowSpecFactory(
		sdk.NewWorkflowParams{},
	)

	cron := croncap.Config{
		Schedule: "*/60 * * * * *", // Every 60 seconds
	}.New(workflow)

	addresses := []string{
		"0x5c25312C82791e6cB76Dc9eFaBE2F5fa695D966b", // Keystone Dev Wallet #1
		"0xAc85bE3811e06712f53BC11844Ed8a37d3e9C3Ab", // Keystone Dev Wallet #2
	}

	// https://sepolia.etherscan.io/address/0x93c4bB995e7B5a726c8ef1bED9EA92e300F18eb4
	balanceReaderContract := "0x93c4bB995e7B5a726c8ef1bED9EA92e300F18eb4"

	chainRead := readcontractcap.Config{
		ContractReaderConfig: `{"contracts":{"BalanceReader":{"contractABI":"[{\\\"inputs\\\":[{\\\"internalType\\\":\\\"address[]\\\",\\\"name\\\":\\\"addresses\\\",\\\"type\\\":\\\"address[]\\\"}],\\\"name\\\":\\\"getNativeBalances\\\",\\\"outputs\\\":[{\\\"internalType\\\":\\\"uint256[]\\\",\\\"name\\\":\\\"\\\",\\\"type\\\":\\\"uint256[]\\\"}],\\\"stateMutability\\\":\\\"view\\\",\\\"type\\\":\\\"function\\\"}]\","contractPollingFilter":{"genericEventNames":null,"pollingFilter":{"topic2":null,"topic3":null,"topic4":null,"retention":"0s","maxLogsKept":0,"logsPerBlock":0}},"configs":{"getNativeBalances":"{  \\\"chainSpecificName\\\": \\\"getNativeBalances\\\"}"}}}}`,
		ContractAddress:      balanceReaderContract,
		ContractName:         "BalanceReader",
		ReadIdentifier:       fmt.Sprintf("%s-%s-%s", balanceReaderContract, "BalanceReader", "getNativeBalances"),
	}.New(
		workflow,
		"read-contract-evm-11155111@1.0.0",
		"read",
		readcontractcap.ActionInput{
			ConfidenceLevel: sdk.ConstantDefinition("finalized"),
			Params: sdk.ConstantDefinition(readcontractcap.InputParams{
				"addresses": addresses,
				"_unused":   cron.Ref(), // TODO: Figure out a nicer way to do this.
			}),
		},
	)
	compConf := computeConfig{
		FeedID: "0x02ce8bfc980000320000000000000000",
	}

	compute := sdk.Compute1WithConfig(
		workflow,
		"compute",
		&sdk.ComputeConfig[computeConfig]{Config: compConf},
		sdk.Compute1Inputs[readcontractcap.Output]{Arg0: chainRead},
		func(runtime sdk.Runtime, config computeConfig, outputs readcontractcap.Output) (computeOutput, error) {
			feedID, err := convertFeedIDtoBytes(config.FeedID)
			if err != nil {
				return computeOutput{}, fmt.Errorf("cannot convert feedID to bytes")
			}

			balances, ok := outputs.LatestValue.([]int64)
			if !ok {
				return computeOutput{}, fmt.Errorf("cannot convert latest value to []int64, got type %T", outputs.LatestValue)
			}

			var balance int64
			for _, bal := range balances {
				balance += bal
			}

			return computeOutput{
				Price:     int(balance),
				FeedID:    feedID, // TrueUSD
				Timestamp: time.Now(),
			}, nil
		},
	)

	consensusInput := ocr3cap.ReduceConsensusInput[computeOutput]{
		Observation: compute.Value(),
	}

	consensus := ocr3cap.ReduceConsensusConfig[computeOutput]{
		Encoder: ocr3cap.EncoderEVM,
		EncoderConfig: map[string]any{
			"abi": "(bytes32 FeedID, uint224 Price, uint32 Timestamp)[] Reports",
		},
		ReportID: "0001",
		KeyID:    "evm",
		AggregationConfig: aggregators.ReduceAggConfig{
			Fields: []aggregators.AggregationField{
				{
					InputKey:  "FeedID",
					OutputKey: "FeedID",
					Method:    "mode",
				},
				{
					InputKey:        "Price",
					OutputKey:       "Price",
					Method:          "median",
					DeviationString: "1",
					DeviationType:   "percent",
				},
				{
					InputKey:        "Timestamp",
					OutputKey:       "Timestamp",
					Method:          "median",
					DeviationString: "300", // 5 minutes
					DeviationType:   "absolute",
				},
			},
			ReportFormat: aggregators.REPORT_FORMAT_ARRAY,
		},
	}.New(workflow, "consensus", consensusInput)

	targetInput := chainwriter.TargetInput{
		SignedReport: consensus,
	}

	chainwriter.TargetConfig{
		Address:    "0x533114FA9E9E9c180e6b898895Bbc31F4D7a4500", // Sepolia PoR Cache
		DeltaStage: "15s",
		Schedule:   "onAtATime",
	}.New(workflow, "write_ethereum-testnet-sepolia@1.0.0", targetInput)

	return workflow
}

func main() {
	runner := wasm.NewRunner()
	workflow := BuildWorkflow(runner.Config())
	runner.Run(workflow)
}
