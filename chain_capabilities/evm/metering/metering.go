package metering

import (
	"fmt"
	"math/big"

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
	limit, err := parseDecimalString(limitStr)
	if err != nil {
		return false, fmt.Errorf("invalid number limit: %w", err)
	}

	actionSpend, err := parseDecimalString(actionSpendStr)
	if err != nil {
		return false, fmt.Errorf("invalid number actionSpend: %w", err)
	}

	return limit.Cmp(actionSpend) >= 0, nil
}

func parseDecimalString(s string) (*big.Rat, error) {
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("strconv.ParseFloat: parsing %q: invalid syntax", s)
	}
	return r, nil
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
