package metering

import (
	"fmt"
	"math/big"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var WriteReportSpendUnitFormat = "GAS.%d" // %d is replaced with the chain selector

func GetResponseMetadataWriteReport(feeAPT *big.Float, chainSelector uint64) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				SpendValue: feeAPT.Text('f', -1),
				SpendUnit:  fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}
