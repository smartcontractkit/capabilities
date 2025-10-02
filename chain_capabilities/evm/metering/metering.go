package metering

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	ActionSpendUnit            = "RPC_EVM"
	WriteReportSpendUnitFormat = "GAS.%d" // %d will be replaced with the chain selector
)

// SpendValueCredits represents the mapping of actions to their spend values.
type SpendValueCredits string

const (
	BalanceAt             SpendValueCredits = "1"
	HeaderByNumber        SpendValueCredits = "1"
	GetTransactionReceipt SpendValueCredits = "1"
	GetTransactionByHash  SpendValueCredits = "1"
	EstimateGas           SpendValueCredits = "1"
	CallContract          SpendValueCredits = "2.5"
	FilterLogs            SpendValueCredits = "2.5"
)

// GetMeteringNodeDetail returns a MeteringNodeDetail for a given SpendValueCredits.
func GetResponseMetadata(action SpendValueCredits) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				//Peer2PeerID will be assigned by the engine, leaving it empty here.
				SpendValue: string(action),
				SpendUnit:  ActionSpendUnit,
			},
		},
	}
}

// GetMeteringNodeDetail returns a MeteringNodeDetail for a given SpendValueCredits.
func GetResponseMetadataWriteReport(fee *big.Float, chainSelector uint64) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				//Peer2PeerID will be assigned by the engine, leaving it empty here.
				SpendValue: fee.Text('f', -1), // have to be stored in eth
				SpendUnit:  fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}

func compareStringNumbers(limitStr, actionSpendStr string) (bool, error) {
	limit, err := strconv.ParseFloat(limitStr, 64)
	if err != nil {
		return false, fmt.Errorf("invalid number limit: %w", err)
	}

	actionSpend, err := strconv.ParseFloat(actionSpendStr, 64)
	if err != nil {
		return false, fmt.Errorf("invalid number actionSpend: %w", err)
	}

	return limit >= actionSpend, nil
}

func CheckHasFunds(lggr logger.SugaredLogger, meta capabilities.RequestMetadata, unit string, actionSpendStr string) error {
	var limitStr string
	for _, spendLimit := range meta.SpendLimits {
		if string(spendLimit.SpendType) == unit {
			limitStr = spendLimit.Limit
			break
		}
	}
	if limitStr == "" {
		lggr.Warnf("no spend limit found for action %s - allowing request", unit)
		return nil
	}
	hasFunds, err := compareStringNumbers(limitStr, actionSpendStr)
	if err != nil {
		return err
	}
	if !hasFunds {
		return fmt.Errorf("insufficient CRE funds: current limit is %s, action spend %s", limitStr, actionSpendStr)
	}
	return nil
}
