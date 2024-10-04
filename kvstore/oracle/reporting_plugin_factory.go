package oracle

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ ocr3types.ReportingPluginFactory[[]byte] = (*reportingPluginFactory)(nil)

type reportingPluginFactory struct {
	logger logger.Logger
}

func NewReportingPluginFactory(logger logger.Logger) *reportingPluginFactory {
	return &reportingPluginFactory{
		logger: logger,
	}
}

func (rpf *reportingPluginFactory) NewReportingPlugin(
	config ocr3types.ReportingPluginConfig,
) (
	ocr3types.ReportingPlugin[[]byte],
	ocr3types.ReportingPluginInfo,
	error,
) {
	return &reportingPlugin{
			logger: rpf.logger,
		}, ocr3types.ReportingPluginInfo{
			Name: "kv-store-oracle@1.0.0",
			Limits: ocr3types.ReportingPluginLimits{

				MaxQueryLength:       ocr3types.MaxMaxQueryLength,
				MaxObservationLength: ocr3types.MaxMaxObservationLength,
				MaxOutcomeLength:     ocr3types.MaxMaxOutcomeLength,
				MaxReportLength:      ocr3types.MaxMaxReportLength,
				MaxReportCount:       ocr3types.MaxMaxReportCount,
			},
		}, nil
}

var _ ocr3types.ReportingPlugin[[]byte] = (*reportingPlugin)(nil)

type reportingPlugin struct {
	logger logger.Logger
}

func (rp *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	return nil, nil
}

func (rp *reportingPlugin) Observation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query) (types.Observation, error) {
	rp.logger.Debug("Observing")
	return nil, nil
}

func (rp *reportingPlugin) ValidateObservation(outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	rp.logger.Debug("Validating observation")
	return nil
}

func (rp *reportingPlugin) ObservationQuorum(outctx ocr3types.OutcomeContext, query types.Query) (ocr3types.Quorum, error) {
	return 1, nil
}

func (rp *reportingPlugin) Outcome(outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (ocr3types.Outcome, error) {
	rp.logger.Debug("Creating an outcome")
	return nil, nil
}

func (rp *reportingPlugin) Reports(seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportWithInfo[[]byte], error) {
	rp.logger.Debug("Reports", "seqNr", seqNr)

	// 2024-10-04T11:02:33.620+0300 [DEBUG] cannot complete, insufficient number of signatures protocol/report_attestation.go:351 configDigest=000192171524191d04b8e50ce8b2019cc4297c595dfa1e2420fb161193e49e2e
	return nil, nil
}

func (rp *reportingPlugin) ShouldAcceptAttestedReport(context.Context, uint64, ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) ShouldTransmitAcceptedReport(context.Context, uint64, ocr3types.ReportWithInfo[[]byte]) (bool, error) {
	return true, nil
}

func (rp *reportingPlugin) Close() error {
	return nil
}
