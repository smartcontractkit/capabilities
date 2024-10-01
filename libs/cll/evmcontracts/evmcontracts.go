package evmcontracts

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/urfave/cli/v2"
)

// deployContract deploys the contract using ABI and bytecode
func deployContract(privateKey string) error {
	cmd := exec.Command(
		"forge", "create",
		"libs/cll/evmcontracts/contracts/keystone/OCR3Capability.sol:OCR3Capability",
		"--rpc-url", "http://127.0.0.1:8545",
		"--private-key", privateKey,
		"--json",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to deploy contract: %v", err)
	}

	fmt.Println("Contract deployed successfully")
	return nil
}

// setContractValue sends a transaction to set a value in the contract
func setContractValue(contractAddress string, privateKey string, value int) error {
	cmd := exec.Command("cast", "send", contractAddress, fmt.Sprintf("set(uint256) %d", value), "--rpc-url", "http://127.0.0.1:8545", "--private-key", privateKey)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to set value in contract: %v", err)
	}

	fmt.Printf("Set value %d in contract %s\n", value, contractAddress)
	return nil
}

// getContractValue reads the stored value from the contract
func getContractValue(contractAddress string) error {
	cmd := exec.Command("cast", "call", contractAddress, "get() (uint256)", "--rpc-url", "http://127.0.0.1:8545")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("failed to get value from contract: %v", err)
	}

	return nil
}

var Commands = []*cli.Command{
	{
		Name:  "contracts",
		Usage: "Commands to manage the EVM contract deployments",
		Subcommands: []*cli.Command{
			{
				Name:  "deploy-ocr",
				Usage: "Deploy a smart contract using ABI and bytecode",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "private-key",
						Usage:    "Private key to use for contract deployment",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					privateKey := c.String("private-key")
					return deployContract(privateKey)
				},
			},
			{
				Name:  "set-value",
				Usage: "Set a value in the deployed contract",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "contract-address",
						Usage:    "Address of the deployed contract",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "private-key",
						Usage:    "Private key to use for sending the transaction",
						Required: true,
					},
					&cli.IntFlag{
						Name:     "value",
						Usage:    "Value to set in the contract",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					contractAddress := c.String("contract-address")
					privateKey := c.String("private-key")
					value := c.Int("value")
					return setContractValue(contractAddress, privateKey, value)
				},
			},
			{
				Name:  "get-value",
				Usage: "Get the stored value from the deployed contract",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "contract-address",
						Usage:    "Address of the deployed contract",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					contractAddress := c.String("contract-address")
					return getContractValue(contractAddress)
				},
			},
		},
	},
}
