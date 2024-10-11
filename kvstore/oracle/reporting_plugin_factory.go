package oracle

import (
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	"github.com/smartcontractkit/capabilities/kvstore/kvrequests"
)

var _ ocr3types.ReportingPluginFactory[[]byte] = (*reportingPluginFactory)(nil)

type reportingPluginFactory struct {
	logger        logger.SugaredLogger
	requestsStore *kvrequests.RequestsStore
}

func NewReportingPluginFactory(
	logger logger.SugaredLogger,
	requestsStore *kvrequests.RequestsStore,
) *reportingPluginFactory {
	return &reportingPluginFactory{
		logger:        logger,
		requestsStore: requestsStore,
	}
}

func (rpf *reportingPluginFactory) NewReportingPlugin(
	config ocr3types.ReportingPluginConfig,
) (
	ocr3types.ReportingPlugin[[]byte],
	ocr3types.ReportingPluginInfo,
	error,
) {
	return NewReportingPlugin(config, rpf.logger, rpf.requestsStore), ocr3types.ReportingPluginInfo{
		Name: "kv-store-oracle",
		Limits: ocr3types.ReportingPluginLimits{
			MaxQueryLength:       ocr3types.MaxMaxQueryLength,
			MaxObservationLength: ocr3types.MaxMaxObservationLength,
			MaxOutcomeLength:     ocr3types.MaxMaxOutcomeLength,
			MaxReportLength:      ocr3types.MaxMaxReportLength,
			MaxReportCount:       ocr3types.MaxMaxReportCount,
		},
	}, nil
}
