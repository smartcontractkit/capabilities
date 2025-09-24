package oracle

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	evmcapocr3types "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/chain-capabilities/consensus/ocr3/types"
)

var _ ocr3types.ReportingPluginFactory[[]byte] = (*ReportingPluginFactory)(nil)

type ReportingPluginFactory struct {
	logger         logger.SugaredLogger
	requestsStore  RequestsHandler
	blocksProvider BlocksProvider
}

func NewReportingPluginFactory(
	logger logger.SugaredLogger,
	requestsStore RequestsHandler,
	blocksProvider BlocksProvider,
) *ReportingPluginFactory {
	return &ReportingPluginFactory{
		logger:         logger,
		requestsStore:  requestsStore,
		blocksProvider: blocksProvider,
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
	offchainCfg, err := readConfig(config.OffchainConfig)
	if err != nil {
		return nil, ocr3types.ReportingPluginInfo{}, fmt.Errorf("failed to read reporting plugin config: %w", err)
	}

	rpf.logger.Infof("Using reporting plugin config: %+v", offchainCfg)

	cfg := Config{
		ReportingPluginConfig: config,
		MaxBatchSize:          int(offchainCfg.MaxBatchSize),
		MaxObservationLength:  int(offchainCfg.MaxObservationLengthBytes),
	}
	return newReportingPlugin(cfg, rpf.logger, rpf.blocksProvider, rpf.requestsStore), ocr3types.ReportingPluginInfo{
		Name: "evm-reads-oracle",
		Limits: ocr3types.ReportingPluginLimits{
			MaxQueryLength:       int(offchainCfg.MaxQueryLengthBytes),
			MaxObservationLength: int(offchainCfg.MaxObservationLengthBytes),
			MaxOutcomeLength:     int(offchainCfg.MaxOutcomeLengthBytes),
			MaxReportLength:      int(offchainCfg.MaxReportLengthBytes),
			MaxReportCount:       int(offchainCfg.MaxReportCount),
		},
	}, nil
}

func readConfig(rawCfg []byte) (*evmcapocr3types.ReportingPluginConfig, error) {
	if len(rawCfg) == 0 {
		const kib = 1024
		const mib = 1024 * kib
		return &evmcapocr3types.ReportingPluginConfig{
			MaxQueryLengthBytes:       mib,
			MaxObservationLengthBytes: 95 * kib, // calculation based on 1 Gbit/s bandwidth, 1s round, 10 nodes. Calculator https://docs.google.com/spreadsheets/d/1ldBQGGT_B2OLdeU5QpTzv30V3HhcMbGCtNE0axRo8sg/edit?gid=1355297791#gid=1355297791
			MaxOutcomeLengthBytes:     ocr3types.MaxMaxOutcomeLength,
			MaxReportLengthBytes:      ocr3types.MaxMaxReportLength,
			MaxReportCount:            ocr3types.MaxMaxReportCount,
			MaxBatchSize:              200,
		}, nil
	}

	var cfg evmcapocr3types.ReportingPluginConfig
	err := proto.Unmarshal(rawCfg, &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal ReportingPluginConfig: %w", err)
	}

	return &cfg, nil
}
