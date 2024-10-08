package evmcontracts

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"

	"github.com/smartcontractkit/capabilities/libs/cli/chain"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

func deployContract() error {
	chainInfo := chain.GetInfo()
	var ocrContractFilePath = filepath.Join(chainInfo.Paths.Contracts, "ocr3_contract_info.json")

	if err := os.MkdirAll(chainInfo.Paths.Contracts, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create smart contracts directory: %v", err)
	}

	chainConfig, err := chain.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get chain config: %v", err)
	}

	outputs, err := utils.ExecCommand("forge", "create",
		"libs/cll/evmcontracts/contracts/keystone/SimpleOCR.sol:SimpleOCR",
		"--rpc-url", chainInfo.URLs.HTTP,
		"--private-key", chainConfig.PrivateKeys[0],
		"--json")

	if err != nil {
		return fmt.Errorf("failed to deploy contract: %v", err)
	}

	var txReceipt struct {
		Deployer        string `json:"deployer"`
		DeployedTo      string `json:"deployedTo"`
		TransactionHash string `json:"transactionHash"`
	}
	if json.Unmarshal(outputs, &txReceipt) != nil {
		return fmt.Errorf("failed to parse contract deployment output: %v", err)
	}

	ocrContractInfo := Info{
		Address: txReceipt.DeployedTo,
	}

	contractInfoJSON, err := json.Marshal(ocrContractInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal contract info: %v", err)
	}

	if os.WriteFile(ocrContractFilePath, contractInfoJSON, 0600) != nil {
		return fmt.Errorf("failed to write contract info to file: %v", err)
	}

	fmt.Printf("Contract deployed to %s (info: %s)\n", ocrContractInfo.Address, ocrContractFilePath)
	return nil
}

func configureContract(nodeIDs []int) error {
	fmt.Printf("Configuring OCR... ")

	chainInfo := chain.GetInfo()
	chainConfig, err := chain.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get chain config: %v", err)
	}
	ocrContractInfo, err := GetInfo("ocr3")
	if err != nil {
		return fmt.Errorf("failed to get OCR contract info: %v", err)
	}

	ocrConfig, err := generateOCR3Config(nodeIDs)
	if err != nil {
		return fmt.Errorf("failed to generate OCR3 config: %v", err)
	}

	var signersArg string
	for _, signer := range ocrConfig.Signers {
		if signersArg != "" {
			signersArg += ","
		}
		signersArg += "0x" + hex.EncodeToString(signer)
	}

	var transmittersArg string
	for _, transmitter := range ocrConfig.Transmitters {
		if transmittersArg != "" {
			transmittersArg += ","
		}
		transmittersArg += string(transmitter)
	}

	_, err = utils.ExecCommand(
		"cast", "send",
		"--rpc-url", chainInfo.URLs.HTTP,
		"--private-key", chainConfig.PrivateKeys[0],
		ocrContractInfo.Address,
		"setConfig(address[], address[], uint8, bytes, uint64, bytes)",
		fmt.Sprintf("[%s]", signersArg),
		fmt.Sprintf("[%s]", transmittersArg),
		fmt.Sprintf("%d", ocrConfig.F),
		"0x"+hex.EncodeToString(ocrConfig.OnchainConfig),
		fmt.Sprintf("%d", ocrConfig.OffchainConfigVersion),
		"0x"+hex.EncodeToString(ocrConfig.OffchainConfig),
	)
	if err != nil {
		return fmt.Errorf("failed to set config in contract: %v", err)
	}
	fmt.Printf("Done.\n")

	return nil
}

var Commands = []*cli.Command{
	{
		Name:  "contracts",
		Usage: "Commands to manage the EVM contract deployments",
		Subcommands: []*cli.Command{
			{
				Name:  "ocr",
				Usage: "OCR smart contract commands",
				Subcommands: []*cli.Command{
					{
						Name:  "deploy",
						Usage: "Deploy OCR3 smart contract",
						Action: func(c *cli.Context) error {
							return deployContract()
						},
					},
					{
						Name:  "configure",
						Usage: "Configure OCR3 smart contract",
						Flags: []cli.Flag{
							&cli.IntSliceFlag{
								Aliases:  []string{"n"},
								Name:     "nodeIDs",
								Usage:    "Node IDs to configure OCR with",
								Required: true,
							},
						},
						Action: func(c *cli.Context) error {
							return configureContract(c.IntSlice("nodeIDs"))
						},
					},
				},
			},
		},
	},
}
