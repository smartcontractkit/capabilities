package metering

import (
	"fmt"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const (
	ActionSpendUnit = "RPC_SOLANA"
)

// SpendValueCredits represents the mapping of actions to their spend values.
type SpendValueCredits string

const (
	GetBalance                SpendValueCredits = "1"
	GetSlotHeight             SpendValueCredits = "1"
	GetBlock                  SpendValueCredits = "1"
	GetTransaction            SpendValueCredits = "1"
	GetFeeForMessage          SpendValueCredits = "1"
	GetSignatureStatuses      SpendValueCredits = "1"
	GetAccountInfoWithOpts    SpendValueCredits = "1"
	GetMultipleAccountsWithOpts SpendValueCredits = "2"
)

// GetResponseMetadata returns a ResponseMetadata for a given SpendValueCredits action.
func GetResponseMetadata(action SpendValueCredits) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				// Peer2PeerID will be assigned by the engine, leaving it empty here.
				SpendValue: string(action),
				SpendUnit:  ActionSpendUnit,
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

// CheckHasFunds verifies that the request has sufficient funds for the action.
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
