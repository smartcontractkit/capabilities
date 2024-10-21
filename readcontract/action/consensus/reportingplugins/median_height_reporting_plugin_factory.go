package reportingplugins

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/capabilities/readcontract/action/consensus/requests"
)

type MedianHeightReportingPluginFactory struct {
	ConsensusHandler *requests.ConsensusHandler
}

func (m *MedianHeightReportingPluginFactory) NewReportingPlugin(ctx context.Context, config ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[[]byte], ocr3types.ReportingPluginInfo, error) {
	return NewMedianHeightReportingPlugin(config, m.ConsensusHandler), ocr3types.ReportingPluginInfo{Name: "MedianHeightReportingPlugin"}, nil
}
