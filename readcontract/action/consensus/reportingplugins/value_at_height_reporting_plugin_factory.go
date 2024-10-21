package reportingplugins

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"
)

type ValueAtHeightReportingPluginFactory struct {
	ConsensusHandler *requests.ConsensusHandler
}

func (v *ValueAtHeightReportingPluginFactory) NewReportingPlugin(ctx context.Context, config ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[[]byte], ocr3types.ReportingPluginInfo, error) {
	return NewValueAtHeightReportingPlugin(config, v.ConsensusHandler, v.ConsensusHandler), ocr3types.ReportingPluginInfo{Name: "ValueAtHeightReportingPlugin"}, nil
}
