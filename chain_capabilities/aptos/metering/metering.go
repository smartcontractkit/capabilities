package metering

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var WriteReportSpendUnitFormat = "GAS.%d" // %d will be replaced with the chain selector

func GetResponseMetadataWriteReport(feeInAPT *big.Float, feeInOctas uint64, chainSelector uint64) capabilities.ResponseMetadata {
	return capabilities.ResponseMetadata{
		Metering: []capabilities.MeteringNodeDetail{
			{
				SpendValue:           feeInAPT.Text('f', -1),
				SpendValueInGasUnits: strconv.FormatUint(feeInOctas, 10),
				SpendUnit:            fmt.Sprintf(WriteReportSpendUnitFormat, chainSelector),
			},
		},
	}
}
