package evmcontracts

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ethereum/go-ethereum/common"
	"github.com/urfave/cli/v2"

	"github.com/smartcontractkit/capabilities/libs/cli/chain"
	"github.com/smartcontractkit/capabilities/libs/cli/constants"
	"github.com/smartcontractkit/capabilities/libs/cli/utils"
)

var smartContractDir = filepath.Join(constants.LocalDir, "chain", "smartcontracts")
var ocrContractFilePath = filepath.Join(smartContractDir, "ocr3_contract_info.json")

// deployContract deploys the contract using ABI and bytecode
func deployContract() error {
	if err := os.MkdirAll(smartContractDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create smart contracts directory: %v", err)
	}

	chainInfo := chain.GetInfo()
	chainConfig, err := chain.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get chain config: %v", err)
	}

	privateKey := chainConfig.PrivateKeys[0]

	outputs, err := utils.ExecCommand("forge", "create",
		"libs/cll/evmcontracts/contracts/keystone/OCR3Capability.sol:OCR3Capability",
		"--rpc-url", chainInfo.URLs.HTTP,
		"--private-key", privateKey,
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

	var contractInfo struct {
		Address string `json:"address"`
	}
	contractInfo.Address = txReceipt.DeployedTo

	contractInfoJSON, err := json.Marshal(contractInfo)
	if err != nil {
		return fmt.Errorf("failed to marshal contract info: %v", err)
	}

	if os.WriteFile(ocrContractFilePath, contractInfoJSON, 0600) != nil {
		return fmt.Errorf("failed to write contract info to file: %v", err)
	}

	fmt.Printf("Contract deployed to %s (info: %s)\n", txReceipt.DeployedTo, ocrContractFilePath)

	ocrConfig, err := generateOCR3Config([]int{2, 3, 4, 5})
	if err != nil {
		return fmt.Errorf("failed to generate OCR3 config: %v", err)
	}

	// type OCR2Config struct {
	// 	Signers               []types.OnchainPublicKey
	// 	Transmitters          []types.Account
	// 	F                     uint8
	// 	OnchainConfig         []byte
	// 	OffchainConfigVersion uint64
	// 	OffchainConfig        []byte
	// }

	// function setConfig(
	//     bytes[] calldata _signers,
	//     address[] calldata _transmitters,
	//     uint8 _f,
	//     bytes memory _onchainConfig,
	//     uint64 _offchainConfigVersion,
	//     bytes memory _offchainConfig
	// )

	var signersArg string
	for _, signer := range ocrConfig.Signers {
		if signersArg != "" {
			signersArg += ","
		}
		signersArg += "0x" + hex.EncodeToString(signer)
	}

	signersArg = "[" + signersArg + "]"

	var transmittersArg string
	for _, transmitter := range ocrConfig.Transmitters {
		if transmittersArg != "" {
			transmittersArg += ","
		}
		transmittersArg += string(transmitter)
	}
	transmittersArg = "[" + transmittersArg + "]"

	functionSignature := "setConfig(bytes[], address[], uint8, bytes, uint64, bytes)"
	// abiEncodeOutput, err := utils.ExecCommand(
	// 	"cast", "abi-encode",
	// 	functionSignature,
	// 	signersArg,
	// 	transmittersArg,
	// 	fmt.Sprintf("%d", ocrConfig.F),
	// 	"0x"+hex.EncodeToString(ocrConfig.OnchainConfig),
	// 	fmt.Sprintf("%d", ocrConfig.OffchainConfigVersion),
	// 	"0x"+hex.EncodeToString(ocrConfig.OffchainConfig),
	// )
	// if err != nil {
	// 	return fmt.Errorf("failed to set config in contract: %v", err)
	// }

	type orc2drOracleConfig struct {
		Signers               [][]byte
		Transmitters          []common.Address
		F                     uint8
		OnchainConfig         []byte
		OffchainConfigVersion uint64
		OffchainConfig        []byte
	}

	fmt.Println("signersArg")
	fmt.Println(signersArg)
	fmt.Println("transmittersArg")
	fmt.Println(transmittersArg)
	fmt.Println("ocrConfig.F")
	fmt.Println(fmt.Sprintf("%d", ocrConfig.F))
	fmt.Println("ocrConfig.OnchainConfig")
	fmt.Println("0x" + hex.EncodeToString(ocrConfig.OnchainConfig))
	fmt.Println("ocrConfig.OffchainConfigVersion")
	fmt.Println(fmt.Sprintf("%d", ocrConfig.OffchainConfigVersion))
	fmt.Println("ocrConfig.OffchainConfig")
	fmt.Println("0x" + hex.EncodeToString(ocrConfig.OffchainConfig))

	setConfigOutput, err := utils.ExecCommand(
		"cast", "send",
		"--rpc-url", chainInfo.URLs.HTTP,
		"--private-key", privateKey,
		txReceipt.DeployedTo,
		functionSignature,
		signersArg,
		transmittersArg,
		fmt.Sprintf("%d", ocrConfig.F),
		"0x"+hex.EncodeToString(ocrConfig.OnchainConfig),
		fmt.Sprintf("%d", ocrConfig.OffchainConfigVersion),
		"0x"+hex.EncodeToString(ocrConfig.OffchainConfig),
	)
	if err != nil {
		return fmt.Errorf("failed to set config in contract: %v", err)
	}
	fmt.Printf("Set config in contract: %s\n", setConfigOutput)

	fmt.Printf("OCR3 config:\n%x\n", ocrConfig)
	return nil
}

var Commands = []*cli.Command{
	{
		Name:  "contracts",
		Usage: "Commands to manage the EVM contract deployments",
		Subcommands: []*cli.Command{
			{
				Name:  "deploy-ocr",
				Usage: "Deploy an OCR smart contract",

				Action: func(c *cli.Context) error {
					return deployContract()
				},
			},
		},
	},
}

// // setContractValue sends a transaction to set a value in the contract
// func setContractValue(contractAddress string, privateKey string, value int) error {
// 	cmd := exec.Command("cast", "send", contractAddress, fmt.Sprintf("set(uint256) %d", value), "--rpc-url", "http://127.0.0.1:8545", "--private-key", privateKey)
// 	cmd.Stdout = os.Stdout
// 	cmd.Stderr = os.Stderr

// 	err := cmd.Run()
// 	if err != nil {
// 		return fmt.Errorf("failed to set value in contract: %v", err)
// 	}

// 	fmt.Printf("Set value %d in contract %s\n", value, contractAddress)
// 	return nil
// }

// // getContractValue reads the stored value from the contract
// func getContractValue(contractAddress string) error {
// 	cmd := exec.Command("cast", "call", contractAddress, "get() (uint256)", "--rpc-url", "http://127.0.0.1:8545")
// 	cmd.Stdout = os.Stdout
// 	cmd.Stderr = os.Stderr

// 	err := cmd.Run()
// 	if err != nil {
// 		return fmt.Errorf("failed to get value from contract: %v", err)
// 	}

// 	return nil
// }
