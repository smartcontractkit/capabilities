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
)

type runtimeConfig struct {
	Addresses []string `yaml:"addresses"`
	Topics    []struct {
		Values []string `yaml:"values"`
	} `yaml:"topics"`
	Abi   string `yaml:"abi"`
	Event string `yaml:"event"`
}

func RunSimpleEvmLogTriggerWorkflow(
	config *runtimeConfig,
	logger *slog.Logger,
	_ cre.SecretsProvider,
) (cre.Workflow[*runtimeConfig], error) {
	_ = logger // not used directly; we log via runtime inside handlers

	cfg := &evm.FilterLogTriggerRequest{
		Addresses: toByteSlices(config.Addresses),
		Topics: []*evm.TopicValues{
			{
				Values: toByteSlices(config.Topics[0].Values),
			},
		},
		Confidence: 1, // LATEST
	}
	return cre.Workflow[*runtimeConfig]{
		cre.Handler(
			evm.LogTrigger(chainselectors.GETH_TESTNET.Selector, cfg),
			onTrigger,
		),
	}, nil
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

func onTrigger(config *runtimeConfig, runtime cre.Runtime, outputs *evm.Log) (string, error) {
	runtime.Logger().With().Info(fmt.Sprintf("OnTrigger called with outputs: %+v", outputs))

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
	wasm.NewRunner(func(b []byte) (*runtimeConfig, error) {
		cfg := &runtimeConfig{}
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("error unmarshalling config: %w", err)
		}
		return cfg, nil
	}).Run(RunSimpleEvmLogTriggerWorkflow)
}
