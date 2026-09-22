package metering

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// SpendValueCredits represents the mapping of read actions to their spend values.
type SpendValueCredits string

const (
	// ReadContract is the placeholder spend value for a ReadContract (simulateTransaction) read.
	// TODO: PLEX-3022 - replace with actual values.
	ReadContract SpendValueCredits = "1"

	// GetLatestLedger is the placeholder spend value for a GetLatestLedger consensus read.
	// TODO: PLEX-3022 - replace with actual values.
	GetLatestLedger SpendValueCredits = "1"

	// WriteReportSpendUnitFormat is the spend unit for write operations, parameterised by chain selector.
	WriteReportSpendUnitFormat = "GAS.%d"
)

// GetResponseMetadata returns the response metadata (metering detail) for a given read action.
func GetResponseMetadata(action SpendValueCredits) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				// Peer2PeerID is assigned by the engine, leaving it empty here.
				SpendValue: string(action),
				SpendUnit:  "RPC_EVM", // TODO: PLEX-3022 - generalize spend unit across chain capabilities
			},
		},
	}
}

// GetResponseMetadataWriteReport returns billing ResponseMetadata for a completed write-report
// submission. feeStroops is the actual FeeCharged from the confirmed transaction in stroops
// (native fixed-point integer, 10^-7 XLM).
// The legacy SpendValue (in XLM) is derived from feeStroops for backwards compatibility.
func GetResponseMetadataWriteReport(feeStroops uint64, chainSelector uint64) capabilities.ResponseMetadata {
	feeInXLM := new(big.Float).Quo(new(big.Float).SetUint64(feeStroops), big.NewFloat(1e7))
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				// Peer2PeerID is assigned by the engine, leaving it empty here.
				SpendValue:           feeInXLM.Text('f', -1),
				SpendValueInGasUnits: strconv.FormatUint(feeStroops, 10),
				SpendUnit:            fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}
