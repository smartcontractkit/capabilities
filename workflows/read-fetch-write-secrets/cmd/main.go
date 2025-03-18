package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ethereum/go-ethereum/common" //nolint:depguard
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/wasm"

	readcontractcap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/actions/readcontract"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/aggregators"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/consensus/ocr3/ocr3cap"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/targets/chainwriter"
	croncap "github.com/smartcontractkit/chainlink-common/pkg/capabilities/triggers/cron"
	"github.com/smartcontractkit/chainlink-common/pkg/workflows/sdk"
)

// Set secrets "cre secrets add DATA_SOURCE_ENDPOINT_URL --secrets-config staging.secrets.config.yaml -S ./workflow.yaml -e ./.env" command
// or manually creating secrets.config.yaml file
// Secret values must be properly encrypted before registering this workflow
// see README for more details on how to do this
const (
	DataSourceEndpoint = "DATA_SOURCE_ENDPOINT_URL"
)

// inject params from `./config/<config>.yaml`
// using cre cli compile with config file
type workflowConfig struct {
	FeedID                      string `yaml:"feed_id" validate:"required,hexadecimal,len=34"` // data ID is bytes16 type (in hex, 0x + 32 chars)
	FeedDescription             string `yaml:"feed_description"`
	CronTriggerInterval         uint8  `yaml:"cron_trigger_interval" validate:"required,gte=1"` // don't allow less than 1 seconds
	EndpointTimeoutMilliseconds uint32 `yaml:"endpoint_timeout_milliseconds" validate:"required,gt=0"`
	DFCacheAddress              string `yaml:"df_cache" validate:"required,hexadecimal,len=42"`       // contract address is bytes20 type (in hex, 0x + 40 chars)
	BalanceReaderAddress        string `yaml:"balance_reader" validate:"required,hexadecimal,len=42"` // contract address is bytes20 type (in hex, 0x + 40 chars)
}

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
	FeedID                      string
	DataSourceEndpoint          sdk.SecretValue
	EndpointTimeoutMilliseconds uint32
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

func BuildWorkflow(runner *wasm.Runner) *sdk.WorkflowSpecFactory {
	// parse workflow config
	var workflowConfig workflowConfig
	err := yaml.Unmarshal(runner.Config(), &workflowConfig)
	if err != nil {
		// proper way to exit is by calling ExitWithError on the runner instance
		// using standard logger and stopping execution with log.Fatal is highly discouraged
		runner.ExitWithError(fmt.Errorf("failed to parse workflow config: %w", err))
	}

	// initiate workflow
	workflow := sdk.NewWorkflowSpecFactory()
	cron := croncap.Config{
		Schedule: fmt.Sprintf("*/%d * * * * *", workflowConfig.CronTriggerInterval),
	}.New(workflow)

	addresses := []common.Address{
		common.HexToAddress("0x5c25312C82791e6cB76Dc9eFaBE2F5fa695D966b"), // Keystone Dev Wallet #1
		common.HexToAddress("0xAc85bE3811e06712f53BC11844Ed8a37d3e9C3Ab"), // Keystone Dev Wallet #2
	}

	chainRead := readcontractcap.Config{
		ContractReaderConfig: `{"contracts":{"BalanceReader":{"contractABI":"[{\"inputs\":[{\"internalType\":\"address[]\",\"name\":\"addresses\",\"type\":\"address[]\"}],\"name\":\"getNativeBalances\",\"outputs\":[{\"internalType\":\"uint256[]\",\"name\":\"\",\"type\":\"uint256[]\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]","contractPollingFilter":{"genericEventNames":null,"pollingFilter":{"topic2":null,"topic3":null,"topic4":null,"retention":"0s","maxLogsKept":0,"logsPerBlock":0}},"configs":{"getNativeBalances":"{  \"chainSpecificName\": \"getNativeBalances\"}"}}}}`,
		ContractAddress:      workflowConfig.BalanceReaderAddress,
		ContractName:         "BalanceReader",
		ReadIdentifier:       fmt.Sprintf("%s-%s-%s", workflowConfig.BalanceReaderAddress, "BalanceReader", "getNativeBalances"),
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
		FeedID:                      workflowConfig.FeedID, // any random bytes16 string to track the feed
		DataSourceEndpoint:          sdk.Secret(DataSourceEndpoint),
		EndpointTimeoutMilliseconds: workflowConfig.EndpointTimeoutMilliseconds,
	}

	compute := sdk.Compute1WithConfig(
		workflow,
		"compute",
		&sdk.ComputeConfig[computeConfig]{Config: compConf},
		sdk.Compute1Inputs[readcontractcap.Output]{Arg0: chainRead},
		func(runtime sdk.Runtime, config computeConfig, output readcontractcap.Output) (computeOutput, error) {

			// example of a system level log
			runtime.Logger().Info("Converting feed ID, ", config.FeedID)
			feedID, err := convertFeedIDtoBytes(config.FeedID)
			if err != nil {
				return computeOutput{}, fmt.Errorf("cannot convert feedID to bytes")
			}

			// READ THE BALANCES
			runtime.Logger().Info("Start reading balances for feed ID ", config.FeedID)
			balances, ok := output.LatestValue.([]any)
			if !ok {
				return computeOutput{}, fmt.Errorf("cannot convert latest value to []*big.Int, got type %T", output.LatestValue)
			}
			runtime.Logger().Info("Balances read for feedID, ", config.FeedID)

			totalBalance := &big.Int{}
			for _, bal := range balances {
				bi, ok := bal.(*big.Int)
				if !ok {
					return computeOutput{}, fmt.Errorf("cannot convert value to *big.Int, got %T", bi)
				}

				totalBalance = totalBalance.Add(totalBalance, bi)
			}
			runtime.Logger().Info("Read balances calculated, ", totalBalance, " for feed ID ", config.FeedID)

			// FETCH THE TRUE USD API
			runtime.Logger().Info("Fetching API", config.DataSourceEndpoint[len(config.DataSourceEndpoint)-7:], " for feed ID ", config.FeedID)
			fresp, err := runtime.Fetch(sdk.FetchRequest{
				URL:       string(config.DataSourceEndpoint),
				Method:    "GET",
				TimeoutMs: config.EndpointTimeoutMilliseconds,
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
			runtime.Logger().Info("Fetched API response, ", resp.TotalTrust)

			// COMPUTE THE TOTAL (by adding the balances to the total value)
			total := resp.TotalTrust + convertBigIntToFloat64(totalBalance)
			runtime.Logger().Info("Total computed, ", total)

			// example of emitting the event to the Beholder, only available in the compute capability
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
		Address:    workflowConfig.DFCacheAddress, // Sepolia PoR/DF Cache 1.5
		DeltaStage: "15s",
		Schedule:   "oneAtATime",
	}.New(workflow, "write_ethereum-testnet-sepolia@1.0.0", targetInput)

	return workflow
}

func main() {
	runner := wasm.NewRunner()
	workflow := BuildWorkflow(runner)
	runner.Run(workflow)
}
