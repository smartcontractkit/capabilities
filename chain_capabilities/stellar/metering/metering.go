package metering

import (
	"fmt"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// SpendValueCredits represents the mapping of read actions to their spend values.
type SpendValueCredits string

const (
	// ReadContract is the placeholder spend value for a ReadContract (simulateTransaction) read.
	// TODO: PLEX-3022 - replace with actual values.
	ReadContract SpendValueCredits = "1"

	// WriteReportSpendUnitFormat is the spend unit for write operations, parameterised by chain selector.
	WriteReportSpendUnitFormat = "STROOP.%d"
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
// submission. feeStroops is the actual FeeCharged from the confirmed transaction.
func GetResponseMetadataWriteReport(feeStroops uint64, chainSelector uint64) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				// Peer2PeerID is assigned by the engine, leaving it empty here.
				SpendValue: strconv.FormatUint(feeStroops, 10),
				SpendUnit:  fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}
