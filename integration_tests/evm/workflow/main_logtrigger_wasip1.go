package main

import (
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	chainselectors "github.com/smartcontractkit/chain-selectors"
	"github.com/smartcontractkit/cre-sdk-go/capabilities/blockchain/evm"
	"github.com/smartcontractkit/cre-sdk-go/cre"
	"github.com/smartcontractkit/cre-sdk-go/cre/wasm"
	"gopkg.in/yaml.v3"

	"github.com/smartcontractkit/capabilities/integration_tests/evm"
)

func RunSimpleEvmLogTriggerWorkflow(
	config *evmlogtrigger.RuntimeConfig,
	logger *slog.Logger,
	_ cre.SecretsProvider,
) (cre.Workflow[*evmlogtrigger.RuntimeConfig], error) {
	_ = logger // not used directly; we log via runtime inside handlers

	topics := []*evm.TopicValues{
		{
			Values: toByteSlices(config.Topics[0].Values),
		},
	}
	for i := 1; i < 4; i++ {
		if i < len(config.Topics) {
			topics = append(topics, &evm.TopicValues{
				Values: toByteSlices(config.Topics[i].Values),
			})
		} else {
			topics = append(topics, &evm.TopicValues{
				Values: [][]byte{},
			})
		}
	}

	cfg := &evm.FilterLogTriggerRequest{
		Addresses:  toByteSlices(config.Addresses),
		Topics:     topics,
		Confidence: evm.ConfidenceLevel(config.Confidence),
	}

	logger.Info(fmt.Sprintf(
		"EVM FilterLogTriggerRequest config: addresses: %v, topics: %v, confidence: %v",
		formatHexSlices(cfg.Addresses),
		formatHexTopics(cfg.Topics),
		cfg.Confidence,
	))

	return cre.Workflow[*evmlogtrigger.RuntimeConfig]{
		cre.Handler(
			evm.LogTrigger(chainselectors.GETH_TESTNET.Selector, cfg),
			onTrigger,
		),
	}, nil
}

// formatHexSlices formats a slice of byte slices as hex strings.
func formatHexSlices(slices [][]byte) []string {
	result := make([]string, len(slices))
	for i, b := range slices {
		result[i] = "0x" + hex.EncodeToString(b)
	}
	return result
}

// formatHexTopics formats a slice of *evm.TopicValues as hex strings.
func formatHexTopics(topics []*evm.TopicValues) [][]string {
	result := make([][]string, len(topics))
	for i, t := range topics {
		result[i] = formatHexSlices(t.Values)
	}
	return result
}

func onTrigger(config *evmlogtrigger.RuntimeConfig, runtime cre.Runtime, outputs *evm.Log) (string, error) {
	runtime.Logger().With().Info(fmt.Sprintf("OnTrigger txHash: %s log index: %d", hex.EncodeToString(outputs.TxHash), outputs.Index))

	decodedMessageString, err := printDecodedData(config.Abi, config.Event, outputs.Data)
	if err != nil {
		runtime.Logger().With().Error(fmt.Sprintf("Error decoding log data: %v", err))
		return "", fmt.Errorf("error decoding log data: %w", err)
	}

	runtime.Logger().With().Info(fmt.Sprintf("OnTrigger decoded message: %s", decodedMessageString))
	return "success", nil
}

func printDecodedData(eventABI string, eventName string, data []byte) (string, error) {
	parsedABI, err := abi.JSON(strings.NewReader(eventABI))
	if err != nil {
		return "", err
	}
	event := parsedABI.Events[eventName]
	values := make(map[string]interface{})
	if err := event.Inputs.UnpackIntoMap(values, data); err != nil {
		return "", err
	}

	var sb strings.Builder
	first := true
	for k, v := range values {
		if !first {
			sb.WriteString("; ")
		}
		sb.WriteString(fmt.Sprintf("%s:%v", k, v))
		first = false
	}
	return sb.String(), nil
}

func main() {
	wasm.NewRunner(func(b []byte) (*evmlogtrigger.RuntimeConfig, error) {
		cfg := &evmlogtrigger.RuntimeConfig{}
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("error unmarshalling config: %w", err)
		}
		return cfg, nil
	}).Run(RunSimpleEvmLogTriggerWorkflow)
}

func toByteSlices(addresses []string) [][]byte {
	result := make([][]byte, len(addresses))
	for i, addr := range addresses {
		// Assumes addresses are hex strings with or without 0x prefix
		b, _ := hex.DecodeString(strings.TrimPrefix(addr, "0x"))
		result[i] = b
	}
	return result
}
