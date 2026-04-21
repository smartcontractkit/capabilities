package metering

import (
	"fmt"
	"math/big"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var WriteReportSpendUnitFormat = "GAS.%d" // %d will be replaced with the chain selector

func GetResponseMetadataWriteReport(fee *big.Float, chainSelector uint64) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				SpendValue: fee.Text('f', -1), // stored in APT
				SpendUnit:  fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}
