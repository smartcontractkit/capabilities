package metering

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// SpendValueCredits represents the mapping of actions to their spend values.
type SpendValueCredits string

const (
	GetAccountInfo SpendValueCredits = "1" // TODO: PLEX-3022  - replace with actual values provided by product
)

var WriteReportSpendUnitFormat = "GAS.%d" // %d will be replaced with the chain selector

// GetResponseMetadataWriteReport returns billing ResponseMetadata for a completed write-report
// submission.
func GetResponseMetadataWriteReport(feeInSol *big.Float, feeInLamports uint64, chainSelector uint64) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				//Peer2PeerID will be assigned by the engine, leaving it empty here.
				SpendValue:           feeInSol.Text('f', -1),
				SpendValueInGasUnits: strconv.FormatUint(feeInLamports, 10),
				SpendUnit:            fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}

// GetResponseMetadata returns a MeteringNodeDetail for a given SpendValueCredits.
func GetResponseMetadata(action SpendValueCredits) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				//Peer2PeerID will be assigned by the engine, leaving it empty here.
				SpendValue: string(action),
				SpendUnit:  "RPC_EVM", // TODO: PLEX-3022 - generalize spend unit across chain capabilities
			},
		},
	}
}
