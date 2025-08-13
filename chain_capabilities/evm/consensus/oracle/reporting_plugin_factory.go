package oracle

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ ocr3types.ReportingPluginFactory[[]byte] = (*ReportingPluginFactory)(nil)

type ReportingPluginFactory struct {
	logger              logger.SugaredLogger
	requestsStore       RequestsHandler
	blocksProvider      BlocksProvider
	batchSize           int
	maxAllowedBatchSize int
}

func NewReportingPluginFactory(
	logger logger.SugaredLogger,
	requestsStore RequestsHandler,
	blocksProvider BlocksProvider,
	batchSize int,
	maxAllowedBatchSize int,
) *ReportingPluginFactory {
	return &ReportingPluginFactory{
		logger:              logger,
		requestsStore:       requestsStore,
		blocksProvider:      blocksProvider,
		batchSize:           batchSize,
		maxAllowedBatchSize: maxAllowedBatchSize,
	}
}

func (rpf *ReportingPluginFactory) NewReportingPlugin(
	_ context.Context,
	config ocr3types.ReportingPluginConfig,
) (
	ocr3types.ReportingPlugin[[]byte],
	ocr3types.ReportingPluginInfo,
	error,
) {
	const maxObservationLength = ocr3types.MaxMaxObservationLength
	cfg := Config{
		ReportingPluginConfig: config,
		BatchSize:             rpf.batchSize,
		MaxAllowedBatchSize:   rpf.maxAllowedBatchSize,
		MaxObservationLength:  maxObservationLength,
	}
	return newReportingPlugin(cfg, rpf.logger, rpf.blocksProvider, rpf.requestsStore), ocr3types.ReportingPluginInfo{
		Name: "evm-reads-oracle",
		Limits: ocr3types.ReportingPluginLimits{
			MaxQueryLength:       ocr3types.MaxMaxQueryLength,
			MaxObservationLength: maxObservationLength,
			MaxOutcomeLength:     ocr3types.MaxMaxOutcomeLength,
			MaxReportLength:      ocr3types.MaxMaxReportLength,
			MaxReportCount:       ocr3types.MaxMaxReportCount,
		},
	}, nil
}
