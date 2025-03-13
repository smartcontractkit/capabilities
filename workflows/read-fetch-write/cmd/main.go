package main

import (
	"encoding/hex"
	"encoding/json"
	"math/big"

	"fmt"
	"time"

	"github.com/ethereum/go-ethereum/common" //nolint:depguard
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm"

	readcontractcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/readcontract"
	croncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/triggers/cron"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/aggregators"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/targets/chainwriter"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
)

// helper internal type that wraps runtime emitter
type WorkflowEventEmitter struct {
	runtime sdk.Runtime
	labels  []string
}

func NewWorkflowEventEmitter(runtime sdk.Runtime) *WorkflowEventEmitter {
	return &WorkflowEventEmitter{
		runtime: runtime,
	}
}

func (e *WorkflowEventEmitter) With(kvs ...string) *WorkflowEventEmitter {
	e.labels = append(e.labels, kvs...)
	return e
}

func (e *WorkflowEventEmitter) Emit(message string) {
	err := e.runtime.Emitter().With(e.labels...).Emit(message)
	if err != nil {
		// using logger instance is not encouraged, and this might be deprecated in the future
		// these logs will not be visible on Beholder, but will end up in node system logs
		e.runtime.Logger().Errorf("failed to emit message: %s", message)
	}
}

type trueUSDResponse struct {
	AccountName string    `json:"accountName"`
	TotalTrust  float64   `json:"totalTrust"`
	TotalToken  float64   `json:"totalToken"`
	Ripcord     bool      `json:"ripcord"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type computeOutput struct {
	TotalTrust uint64
	TotalToken uint64
	Ripcord    bool
	FeedID     [32]byte
	Timestamp  int64
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

func convertBigIntToFloat64(bi *big.Int) float64 {
	bigFloat := new(big.Float).SetInt(bi)
	f, _ := bigFloat.Float64()
	return f
}

func BuildWorkflow(config []byte) *sdk.WorkflowSpecFactory {
	workflow := sdk.NewWorkflowSpecFactory()

	cron := croncap.Config{
		Schedule: "*/60 * * * * *", // Every 60 seconds
	}.New(workflow)

	addresses := []common.Address{
		common.HexToAddress("0x5c25312C82791e6cB76Dc9eFaBE2F5fa695D966b"), // Keystone Dev Wallet #1
		common.HexToAddress("0xAc85bE3811e06712f53BC11844Ed8a37d3e9C3Ab"), // Keystone Dev Wallet #2
	}

	// https://sepolia.etherscan.io/address/0x93c4bB995e7B5a726c8ef1bED9EA92e300F18eb4
	balanceReaderContract := "0x93c4bB995e7B5a726c8ef1bED9EA92e300F18eb4"

	chainRead := readcontractcap.Config{
		ContractReaderConfig: `{"contracts":{"BalanceReader":{"contractABI":"[{\"inputs\":[{\"internalType\":\"address[]\",\"name\":\"addresses\",\"type\":\"address[]\"}],\"name\":\"getNativeBalances\",\"outputs\":[{\"internalType\":\"uint256[]\",\"name\":\"\",\"type\":\"uint256[]\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]","contractPollingFilter":{"genericEventNames":null,"pollingFilter":{"topic2":null,"topic3":null,"topic4":null,"retention":"0s","maxLogsKept":0,"logsPerBlock":0}},"configs":{"getNativeBalances":"{  \"chainSpecificName\": \"getNativeBalances\"}"}}}}`,
		ContractAddress:      balanceReaderContract,
		ContractName:         "BalanceReader",
		ReadIdentifier:       fmt.Sprintf("%s-%s-%s", balanceReaderContract, "BalanceReader", "getNativeBalances"),
	}.New(
		workflow,
		"read-contract-evm-11155111@1.0.0",
		"readETHTest",
		readcontractcap.ActionInput{
			ConfidenceLevel: sdk.ConstantDefinition("unconfirmed"),
			Params: sdk.ConstantDefinition(readcontractcap.InputParams{
				"addresses": addresses,
			}),
			StepDependency: sdk.ConstantDefinition(cron.Ref()),
		},
	)

	compConf := computeConfig{
		FeedID: "0x746573745F6561610000000000000000", // any random bytes16 string to track the feed
	}

	compute := sdk.Compute1WithConfig(
		workflow,
		"compute",
		&sdk.ComputeConfig[computeConfig]{Config: compConf},
		sdk.Compute1Inputs[readcontractcap.Output]{Arg0: chainRead},
		func(runtime sdk.Runtime, config computeConfig, output readcontractcap.Output) (computeOutput, error) {
			// example of emitting, emit the event to the Beholder, only available in the compute capability
			NewWorkflowEventEmitter(runtime).
				With("feedID", config.FeedID).
				Emit(fmt.Sprintf("Converting feedID, %s", config.FeedID))

			// system level log
			runtime.Logger().Info("Converting feedID:", config.FeedID)

			feedID, err := convertFeedIDtoBytes(config.FeedID)
			if err != nil {
				return computeOutput{}, fmt.Errorf("cannot convert feedID to bytes")
			}

			// READ THE BALANCES
			runtime.Logger().Info("Start reading balances")
			balances, ok := output.LatestValue.([]any)
			if !ok {
				return computeOutput{}, fmt.Errorf("cannot convert latest value to []*big.Int, got type %T", output.LatestValue)
			}

			NewWorkflowEventEmitter(runtime).
				With("feedID", config.FeedID).
				Emit(fmt.Sprintf("Balances read, %s", config.FeedID))

			// system level log
			runtime.Logger().Info("Balances read,", config.FeedID)

			totalBalance := &big.Int{}
			for _, bal := range balances {
				bi, ok := bal.(*big.Int)
				if !ok {
					return computeOutput{}, fmt.Errorf("cannot convert value to *big.Int, got %T", bi)
				}

				totalBalance = totalBalance.Add(totalBalance, bi)
			}

			// system level log
			runtime.Logger().Info("Read balances: ", totalBalance)

			// FETCH THE TRUE USD API
			runtime.Logger().Info("Fetching API")
			fresp, err := runtime.Fetch(sdk.FetchRequest{
				URL:       "https://api.real-time-reserves.verinumus.io/v1/chainlink/proof-of-reserves/TrueUSD",
				Method:    "GET",
				TimeoutMs: 5000,
			})
			if err != nil {
				return computeOutput{}, fmt.Errorf("not able to fetch API response: %w", err)
			}

			var resp trueUSDResponse
			err = json.Unmarshal(fresp.Body, &resp)
			if err != nil {
				return computeOutput{}, fmt.Errorf("not able to unmarshal response payload %s, err: %w", fresp.Body, err)
			}

			if resp.Ripcord {
				err := runtime.Emitter().
					With("feedID", config.FeedID).
					Emit(fmt.Sprintf("ripcord flag set for feed ID %s", config.FeedID))
				if err != nil {
					runtime.Logger().Error("Failed to emit event for fetched HTTP response")
				}
			}

			// system level log
			runtime.Logger().Info("Fetched API response: ", resp.TotalTrust)

			// COMPUTE THE TOTAL (by adding the balances to the total value)
			total := resp.TotalTrust + convertBigIntToFloat64(totalBalance)

			// system level log
			runtime.Logger().Info("Total computed: ", total)

			NewWorkflowEventEmitter(runtime).
				With("feedID", config.FeedID).
				Emit(fmt.Sprintf("Total computed for feed ID %s", config.FeedID))

			return computeOutput{
				TotalTrust: uint64(total * 100),           // 2 decimal places
				TotalToken: uint64(resp.TotalToken * 100), // 2 decimal places
				Ripcord:    resp.Ripcord,                  // 0 decimal places
				FeedID:     feedID,
				Timestamp:  resp.UpdatedAt.Unix(),
			}, nil
		},
	)

	consensusInput := ocr3cap.ReduceConsensusInput[computeOutput]{
		Observation: compute.Value(),
	}

	consensus := ocr3cap.ReduceConsensusConfig[computeOutput]{
		Encoder: ocr3cap.EncoderEVM,
		EncoderConfig: map[string]any{
			"abi": "(bytes32 FeedID, uint32 Timestamp, bytes Bundle)[] Reports",
			"subabi": map[string]string{
				"Reports.Bundle": "uint256 TotalTrust, uint256 TotalToken, bool Ripcord",
			},
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
					InputKey:        "Timestamp",
					OutputKey:       "Timestamp",
					Method:          "median",
					DeviationString: "300", // 5 minutes to write on-chain
					DeviationType:   "absolute",
				},
				{
					InputKey:        "TotalTrust",
					OutputKey:       "TotalTrust",
					Method:          "median",
					DeviationString: "1",
					DeviationType:   "percent",
					SubMapField:     true,
				},
				{
					InputKey:        "TotalToken",
					OutputKey:       "TotalToken",
					Method:          "median",
					DeviationString: "1",
					DeviationType:   "percent",
					SubMapField:     true,
				},
				{
					InputKey:    "Ripcord",
					OutputKey:   "Ripcord",
					Method:      "mode",
					SubMapField: true,
				},
			},
			ReportFormat: aggregators.REPORT_FORMAT_ARRAY,
			SubMapKey:    "Bundle",
		},
	}.New(workflow, "consensus", consensusInput)

	targetInput := chainwriter.TargetInput{
		SignedReport: consensus,
	}

	chainwriter.TargetConfig{
		Address:    "0xb79288ce6a58b7af2230a77f296f6a13b78a0292", // Sepolia PoR Cache using DF 1.5
		DeltaStage: "15s",
		Schedule:   "oneAtATime",
	}.New(workflow, "write_ethereum-testnet-sepolia@1.0.0", targetInput)

	return workflow
}

func main() {
	runner := wasm.NewRunner()
	workflow := BuildWorkflow(runner.Config())
	runner.Run(workflow)
}
