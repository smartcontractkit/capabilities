package main

import (
	"encoding/hex"
	"fmt"
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

func RunSimpleEvmLogTriggerWorkflow(env *cre.Environment[*runtimeConfig]) (cre.Workflow[*runtimeConfig], error) {
	fmt.Println("RunSimpleEvmLogTriggerWorkflow called")

	cfg := &evm.FilterLogTriggerRequest{
		Addresses: toByteSlices(env.Config.Addresses),
		Topics: []*evm.TopicValues{
			{
				Values: toByteSlices(env.Config.Topics[0].Values),
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

func onTrigger(env *cre.Environment[*runtimeConfig], _ cre.Runtime, outputs *evm.Log) (string, error) {
	fmt.Println("OnTrigger called with outputs:", outputs)
	decodedMessageString, err := printDecodedData(env.Config.Abi, env.Config.Event, outputs.Data)
	if err != nil {
		fmt.Println("OnTrigger error:", err)
		return "", fmt.Errorf("error decoding log data: %w", err)
	}
	fmt.Println("OnTrigger called with decodedMessageString:", decodedMessageString)
	env.Logger.Info(fmt.Sprintf("OnTrigger decoded message: %s", decodedMessageString))
	return "success", nil
}

func printDecodedData(eventABI string, eventName string, data []byte) (string, error) {
	parsedABI, err := abi.JSON(strings.NewReader(eventABI))
	if err != nil {
		return "", err
	}
	event := parsedABI.Events[eventName]
	values := make(map[string]interface{})
	err = event.Inputs.UnpackIntoMap(values, data)
	if err != nil {
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
		if err := yaml.Unmarshal(b, &cfg); err != nil {
			return nil, fmt.Errorf("error unmarshalling config: %w", err)
		}
		return cfg, nil
	}).Run(RunSimpleEvmLogTriggerWorkflow)
}
