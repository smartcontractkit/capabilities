package oracle

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

var _ ocr3types.ReportingPluginFactory[[]byte] = (*reportingPluginFactory)(nil)

type reportingPluginFactory struct{}

func NewReportingPluginFactory() *reportingPluginFactory {
	return &reportingPluginFactory{}
}

func (rpf *reportingPluginFactory) NewReportingPlugin(
	config ocr3types.ReportingPluginConfig,
) (
	ocr3types.ReportingPlugin[[]byte],
	ocr3types.ReportingPluginInfo,
	error,
) {
	return &reportingPlugin{}, ocr3types.ReportingPluginInfo{
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
}

func (rp *reportingPlugin) Query(ctx context.Context, outctx ocr3types.OutcomeContext) (types.Query, error) {
	return nil, nil
}

func (rp *reportingPlugin) Observation(ctx context.Context, outctx ocr3types.OutcomeContext, query types.Query) (types.Observation, error) {
	return nil, nil
}

func (rp *reportingPlugin) ValidateObservation(outctx ocr3types.OutcomeContext, query types.Query, ao types.AttributedObservation) error {
	return nil
}

func (rp *reportingPlugin) ObservationQuorum(outctx ocr3types.OutcomeContext, query types.Query) (ocr3types.Quorum, error) {
	return 1, nil
}

func (rp *reportingPlugin) Outcome(outctx ocr3types.OutcomeContext, query types.Query, aos []types.AttributedObservation) (ocr3types.Outcome, error) {
	return nil, nil
}

func (rp *reportingPlugin) Reports(seqNr uint64, outcome ocr3types.Outcome) ([]ocr3types.ReportWithInfo[[]byte], error) {
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
