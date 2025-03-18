package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
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

const (
	DataSourceEndpoint = "https://api.superstate.co/v1/funds/2/nav-daily"
)

// inject params from `./config/config.yaml`
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

type FundData struct {
	FundID                int    `json:"fund_id"`
	NetAssetValueDate     string `json:"net_asset_value_date"`
	NetAssetValue         string `json:"net_asset_value"`
	AssetsUnderManagement string `json:"assets_under_management"`
	OutstandingShares     string `json:"outstanding_shares"`
	NetIncomeExpenses     string `json:"net_income_expenses"`
}

type computeOutput struct {
	NetAssetValue         uint64
	AssetsUnderManagement uint64
	OutstandingShares     uint64
	NetIncomeExpenses     uint64
	FeedID                [32]byte
	Timestamp             int64
}

type computeConfig struct {
	FeedID                      string
	DataSourceEndpoint          string
	EndpointTimeoutMilliseconds uint32
}

func parseFloat(value string) float64 {
	f, _ := strconv.ParseFloat(value, 64) // handle errors as needed
	return f
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

	// CRON TRIGGER
	cron := croncap.Config{
		Schedule: fmt.Sprintf("*/%d * * * * *", workflowConfig.CronTriggerInterval),
	}.New(workflow)

	addresses := []common.Address{
		common.HexToAddress("0x5c25312C82791e6cB76Dc9eFaBE2F5fa695D966b"), // Test Wallet #1
		common.HexToAddress("0xAc85bE3811e06712f53BC11844Ed8a37d3e9C3Ab"), // Test Wallet #2
	}

	// READ THE BALANCES
	chainRead := readcontractcap.Config{
		ContractReaderConfig: `{"contracts":{"BalanceReader":{"contractABI":"[{\"inputs\":[{\"internalType\":\"address[]\",\"name\":\"addresses\",\"type\":\"address[]\"}],\"name\":\"getNativeBalances\",\"outputs\":[{\"internalType\":\"uint256[]\",\"name\":\"\",\"type\":\"uint256[]\"}],\"stateMutability\":\"view\",\"type\":\"function\"}]","contractPollingFilter":{"genericEventNames":null,"pollingFilter":{"topic2":null,"topic3":null,"topic4":null,"retention":"0s","maxLogsKept":0,"logsPerBlock":0}},"configs":{"getNativeBalances":"{  \"chainSpecificName\": \"getNativeBalances\"}"}}}}`,
		ContractAddress:      workflowConfig.BalanceReaderAddress,
		ContractName:         "BalanceReader",
		ReadIdentifier:       fmt.Sprintf("%s-%s-%s", workflowConfig.BalanceReaderAddress, "BalanceReader", "getNativeBalances"),
	}.New(
		workflow,
		"read-contract-evm-11155111@1.0.0",
		"readETHSup",
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
		DataSourceEndpoint:          string(DataSourceEndpoint),
		EndpointTimeoutMilliseconds: workflowConfig.EndpointTimeoutMilliseconds,
	}

	// COMPUTE
	compute := sdk.Compute1WithConfig(
		workflow,
		"compute",
		&sdk.ComputeConfig[computeConfig]{Config: compConf},
		sdk.Compute1Inputs[readcontractcap.Output]{Arg0: chainRead},
		func(runtime sdk.Runtime, config computeConfig, output readcontractcap.Output) (computeOutput, error) {
			// beholder event emitter
			NewWorkflowEventEmitter(runtime).
				With("feedID", config.FeedID).
				With("URL", config.DataSourceEndpoint).
				Emit(fmt.Sprintf("Starting workflow for feed ID: %s", config.FeedID))

			// example of a system level log
			runtime.Logger().Info("Converting feed ID, ", config.FeedID)
			feedID, err := convertFeedIDtoBytes(config.FeedID)
			if err != nil {
				return computeOutput{}, fmt.Errorf("cannot convert feedID to bytes")
			}

			// READ THE BALANCES
			runtime.Logger().Info("Getting balance for feed ID ", config.FeedID)
			balances, ok := output.LatestValue.([]any)
			if !ok {
				return computeOutput{}, fmt.Errorf("cannot convert latest value to []*big.Int, got type %T", output.LatestValue)
			}
			runtime.Logger().Info("Balances obtained for feedID, ", config.FeedID)

			totalBalance := &big.Int{}
			for _, bal := range balances {
				bi, ok := bal.(*big.Int)
				if !ok {
					return computeOutput{}, fmt.Errorf("cannot convert value to *big.Int, got %T", bi)
				}

				totalBalance = totalBalance.Add(totalBalance, bi)
			}
			runtime.Logger().Info("Balances calculated, ", totalBalance, " for feed ID ", config.FeedID)

			// FETCH THE SUPERSTATE API
			truncatedDSEndpoint := config.DataSourceEndpoint[len(config.DataSourceEndpoint)-7:]
			runtime.Logger().Info("Fetching API", truncatedDSEndpoint, " for feed ID ", config.FeedID)
			fresp, err := runtime.Fetch(sdk.FetchRequest{
				URL:       config.DataSourceEndpoint,
				Method:    "GET",
				TimeoutMs: config.EndpointTimeoutMilliseconds,
			})
			if err != nil {
				return computeOutput{}, fmt.Errorf("not able to fetch API response: %w", err)
			}

			var resp []FundData
			err = json.Unmarshal(fresp.Body, &resp)
			if err != nil {
				return computeOutput{}, fmt.Errorf("not able to unmarshal response payload %s, err: %w", fresp.Body, err)
			}
			runtime.Logger().Info("Fetched API response, NetAssetValueDate ", resp[0].NetAssetValueDate, " for feed ID ", config.FeedID)

			NewWorkflowEventEmitter(runtime).
				With("feedID", config.FeedID).
				With("URL", config.DataSourceEndpoint).
				With("NetAssetValue", resp[0].NetAssetValue).
				Emit(fmt.Sprintf("received response payload: %s", fresp.Body))

			// An example of computing the total (by adding the balances to the "total" value)
			total := parseFloat(resp[0].NetAssetValue) + convertBigIntToFloat64(totalBalance)
			runtime.Logger().Info("Total computed, ", total, " for feed ID ", config.FeedID)

			return computeOutput{
				NetAssetValue:         uint64(total * 100000000),                                     // 8 decimal places
				AssetsUnderManagement: uint64(parseFloat(resp[0].AssetsUnderManagement) * 100000000), // 8 decimal places
				OutstandingShares:     uint64(parseFloat(resp[0].OutstandingShares) * 100000000),     // 8 decimal places
				NetIncomeExpenses:     uint64(parseFloat(resp[0].NetIncomeExpenses) * 100000000),     // 8 decimal places
				FeedID:                feedID,
				Timestamp:             time.Now().Unix(),
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
				"Reports.Bundle": "uint256 NetAssetValue, uint256 AssetsUnderManagement, uint256 OutstandingShares,uint256 NetIncomeExpenses",
			},
		},
		ReportID: "0010",
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
					DeviationString: "300",
					DeviationType:   "absolute",
				},
				{
					InputKey:        "NetAssetValue",
					OutputKey:       "NetAssetValue",
					Method:          "median",
					DeviationString: "5",
					DeviationType:   "percent",
					SubMapField:     true,
				},
				{
					InputKey:        "AssetsUnderManagement",
					OutputKey:       "AssetsUnderManagement",
					Method:          "median",
					DeviationString: "1",
					DeviationType:   "percent",
					SubMapField:     true,
				},
				{
					InputKey:        "OutstandingShares",
					OutputKey:       "OutstandingShares",
					Method:          "median",
					DeviationString: "1",
					DeviationType:   "percent",
					SubMapField:     true,
				},
				{
					InputKey:        "NetIncomeExpenses",
					OutputKey:       "NetIncomeExpenses",
					Method:          "median",
					DeviationString: "1",
					DeviationType:   "percent",
					SubMapField:     true,
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
