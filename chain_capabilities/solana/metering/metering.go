package metering

import (
	"fmt"
	"math/big"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// SpendValueCredits represents the mapping of actions to their spend values.
type SpendValueCredits string

var WriteReportSpendUnitFormat = "GAS.%d" // %d will be replaced with the chain selector

// GetMeteringNodeDetail returns a MeteringNodeDetail for a given SpendValueCredits.
func GetResponseMetadataWriteReport(fee *big.Float, chainSelector uint64) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				//Peer2PeerID will be assigned by the engine, leaving it empty here.
				SpendValue: fee.Text('f', -1), // have to be stored in SOL
				SpendUnit:  fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}
