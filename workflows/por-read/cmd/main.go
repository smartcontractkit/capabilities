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
		ContractReaderConfig: "{\"chainId\": 11155111,\"network\": \"evm\"}",
		Address:              balanceReaderContract,
		ReadIdentifier:       "BalanceReader.getNativeBalances", // TODO
	}.New(
		workflow,
		"read-contract-evm-11155111@1.0.0",
		"read",
		readcontractcap.ActionInput{
			ConfidenceLevel: sdk.ConstantDefinition("finalized"),
			Params: sdk.ConstantDefinition(readcontractcap.InputParams{
				"addresses": addresses,
				"_unused":   cron.Ref(), // Figure out a nicer way to do this.
			}),
		},
	)

	compConf := computeConfig{
		FeedID: "", // TODO
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
					DeviationString: "86400", // 24 hours
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
		Address: "0x83aF34AbeF5785Dc9C65C2e581f773e5c722fDe0", // Eth sepolia feeds consumer
	}.New(workflow, "write_chain", targetInput)

	return workflow
}

func main() {
	runner := wasm.NewRunner()
	workflow := BuildWorkflow(runner.Config())
	runner.Run(workflow)
}
