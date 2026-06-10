package metering

import (
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// SpendValueCredits represents the mapping of read actions to their spend values.
type SpendValueCredits string

const (
	// ReadContract is the placeholder spend value for a ReadContract (simulateTransaction) read.
	// TODO: PLEX-3022 - replace with actual values.
	ReadContract SpendValueCredits = "1"
)

// GetResponseMetadata returns the response metadata (metering detail) for a given read action.
func GetResponseMetadata(action SpendValueCredits) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				// Peer2PeerID is assigned by the engine, leaving it empty here.
				SpendValue: string(action),
				SpendUnit:  "RPC_STELLAR", // TODO: PLEX-3022 - generalize spend unit across chain capabilities
			},
		},
	}
}
