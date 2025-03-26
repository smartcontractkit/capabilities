package main

import (
	"encoding/hex"
	"math/big"

	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm"

	readcontractcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/readcontract"
	croncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/triggers/cron"

	"github.com/ethereum/go-ethereum/common"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/aggregators"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/targets/chainwriter"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
)

type computeOutput struct {
	Price     *big.Int
	FeedID    [32]byte
	Timestamp int64
}

type computeConfig struct {
	FeedID string
}

func convertFeedIDtoBytes(feedIDStr string) ([32]byte, error) {
	b, err := hex.DecodeString(feedIDStr[2:])
	if err != nil {
		return [32]byte{}, err
	}

	if len(b) < 32 {
		nb := [32]byte{}
		copy(nb[:], b[:])
		return nb, err
	}

	return [32]byte(b), nil
}

func BuildWorkflow(config []byte) *sdk.WorkflowSpecFactory {
	workflow := sdk.NewWorkflowSpecFactory()

	cron := croncap.Config{
		Schedule: "*/60 * * * * *", // Every 60 seconds
	}.New(workflow)

	addresses := []common.Address{
		common.HexToAddress("0xFbb30BD8E9D779044c3c30dd82e52a5FA1573388"),
	}

	// hello
	// Base
	addr := "0xf9C805641348dE519210e4A2CA6bd7EE0DAC051f"

	chainRead := readcontractcap.Config{
		ContractReaderConfig: `{"contracts":{"BalanceReader":{"contractABI":"[{\"inputs\":[{\"internalType\":\"address[]\",\"name\":\"addresses\",\"type\":\"address[]\"}],\"name\":\"getNativeBalances\",\"outputs\":[{\"internalType\":\"uint256[]\",\"name\":\"\",\"type\":\"uint256[]\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]","contractPollingFilter":{"genericEventNames":null,"pollingFilter":{"topic2":null,"topic3":null,"topic4":null,"retention":"0s","maxLogsKept":0,"logsPerBlock":0}},"configs":{"getNativeBalances":"{  \"chainSpecificName\": \"getNativeBalances\"}"}}}}`,
		ContractAddress:      addr,
		ContractName:         "BalanceReader",
		ReadIdentifier:       fmt.Sprintf("%s-%s-%s", addr, "BalanceReader", "getNativeBalances"),
	}.New(
		workflow,
		"read-contract-evm-8453@1.0.0",
		"read",
		readcontractcap.ActionInput{
			ConfidenceLevel: sdk.ConstantDefinition("unconfirmed"),
			Params: sdk.ConstantDefinition(readcontractcap.InputParams{
				"addresses": addresses,
			}),
			StepDependency: sdk.ConstantDefinition(cron.Ref()),
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

			balances, ok := outputs.LatestValue.([]any)
			if !ok {
				return computeOutput{}, fmt.Errorf("cannot convert latest value to []*big.Int, got type %T", outputs.LatestValue)
			}

			balance := &big.Int{}
			for _, bal := range balances {
				bi, ok := bal.(*big.Int)
				if !ok {
					return computeOutput{}, fmt.Errorf("cannot convert value to *big.Int, got %T", bi)
				}

				balance = balance.Add(balance, bi)
			}

			return computeOutput{
				Price:     balance,
				FeedID:    feedID, // Randomly generated
				Timestamp: time.Now().Unix(),
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
		Address:    "0xf8CEdCB201DA4dDd163cb845aE5220624E96D1B0",
		DeltaStage: "60s",
		Schedule:   "oneAtATime",
	}.New(workflow, "write_ethereum-mainnet-base-1@1.0.0", targetInput)

	return workflow
}

func main() {
	runner := wasm.NewRunner()
	workflow := BuildWorkflow(runner.Config())
	runner.Run(workflow)
}
