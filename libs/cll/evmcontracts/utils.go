package evmcontracts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/smartcontractkit/capabilities/libs/cli/chain"
)

type Info struct {
	Address string `json:"address"`
}

func GetInfo(contractName string) (*Info, error) {
	chainInfo := chain.GetInfo()

	fileData, err := os.ReadFile(filepath.Join(
		chainInfo.Paths.Contracts,
		fmt.Sprintf("%s_contract_info.json", contractName),
	))
	if err != nil {
		return nil, fmt.Errorf("failed to read contract info file: %v", err)
	}

	var contractInfo Info
	if err = json.Unmarshal(fileData, &contractInfo); err != nil {
		return nil, fmt.Errorf("failed to unmarshal contract info JSON: %v", err)
	}

	return &contractInfo, nil
}
