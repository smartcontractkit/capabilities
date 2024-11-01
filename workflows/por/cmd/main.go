package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm"

	"github.com/smartcontractkit/capabilities/cron/croncap"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/aggregators"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/targets/chainwriter"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
)

type trueUSDResponse struct {
	AccountName string    `json:"accountName"`
	TotalTrust  float64   `json:"totalTrust"`
	Ripcord     bool      `json:"ripcord"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

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
		Schedule: "*/30 * * * * *",
	}.New(workflow)

	compConf := computeConfig{
		FeedID: "",
	}

	compute := sdk.Compute1WithConfig(
		workflow,
		"compute",
		&sdk.ComputeConfig[computeConfig]{Config: compConf},
		sdk.Compute1Inputs[croncap.Payload]{Arg0: cron},
		func(runtime sdk.Runtime, config computeConfig, outputs croncap.Payload) (computeOutput, error) {
			feedID, err := convertFeedIDtoBytes(config.FeedID)
			if err != nil {
				return computeOutput{}, fmt.Errorf("cannot convert feedID to bytes")
			}

			fresp, err := runtime.Fetch(sdk.FetchRequest{
				URL:    "https://api.real-time-reserves.verinumus.io/v1/chainlink/proof-of-reserves/TrueUSD",
				Method: "GET",
			})
			if err != nil {
				return computeOutput{}, err
			}

			var resp trueUSDResponse
			err = json.Unmarshal(fresp.Body, &resp)
			if err != nil {
				return computeOutput{}, err
			}

			if resp.Ripcord {
				err := runtime.Emitter().With(
					"feedID", config.FeedID,
				).Emit(fmt.Sprintf("ripcord flag set for feed ID %s", config.FeedID))
				if err != nil {
					runtime.Logger().Error("failed to emit custom message")
				}
				return computeOutput{}, sdk.BreakErr
			}

			return computeOutput{
				Price:     int(resp.TotalTrust * 100),
				FeedID:    feedID, // TrueUSD
				Timestamp: resp.UpdatedAt,
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
					DeviationString: "300",
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
