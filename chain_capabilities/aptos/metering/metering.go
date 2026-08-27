package metering

import (
	"fmt"
	"math/big"
	"strconv"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

var WriteReportSpendUnitFormat = "GAS.%d" // %d will be replaced with the chain selector

// GetResponseMetadataWriteReport returns billing ResponseMetadata for a completed write-report
// submission. feeInOctas is the transaction fee in octas (native fixed-point integer).
// The legacy SpendValue (in APT) is derived from feeInOctas for backwards compatibility.
func GetResponseMetadataWriteReport(feeInOctas uint64, chainSelector uint64) capabilities.ResponseMetadata {
	feeInAPT := new(big.Float).Quo(new(big.Float).SetUint64(feeInOctas), big.NewFloat(1e8))
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
