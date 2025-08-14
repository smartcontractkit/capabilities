package metering

import (
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

const SpendUnit = "EVM RPC Call"

// SpendValueEnum represents the mapping of actions to their spend values.
type SpendValueEnum string

const (
	BalanceAt             SpendValueEnum = "0.00001"
	HeaderByNumber        SpendValueEnum = "0.00001"
	GetTransactionReceipt SpendValueEnum = "0.00001"
	GetTransactionByHash  SpendValueEnum = "0.00001"
	EstimateGas           SpendValueEnum = "0.00001"
	CallContract          SpendValueEnum = "0.000025"
)

// GetMeteringNodeDetail returns a MeteringNodeDetail for a given SpendValueEnum.
func GetResponseMetadata(action SpendValueEnum) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				//Peer2PeerID will be assigned by the engine, leaving it empty here.
				SpendValue: string(action),
				SpendUnit:  SpendUnit,
			},
		},
	}
}
