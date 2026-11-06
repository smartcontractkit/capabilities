package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/capabilities/internal/consensus/oracle/plugin"
	oracletypes "github.com/smartcontractkit/capabilities/internal/consensus/oracle/types"
)

var failedInfo = plugin.ReportInfo{
	KeyBundle: "aptos",
	Value: plugin.Info{
		RequestID: "request",
		Failure:   &plugin.Failure{Code: oracletypes.ConsensusFailureCode_RECEIVED_FPLUS1_ERRORS, Message: "too many errors"},
	},
}

func TestInfo_RoundTrips(t *testing.T) {
	for _, info := range []plugin.ReportInfo{failedInfo, {KeyBundle: "evm", Value: plugin.Info{RequestID: "ok"}}} {
		encoded, err := encodeInfo(info)
		require.NoError(t, err)

		decoded, err := decodeReport(ocr3types.ReportWithInfo[[]byte]{Report: []byte("report"), Info: encoded})
		require.NoError(t, err)
		assert.Equal(t, []byte("report"), []byte(decoded.Report))
		assert.Equal(t, info, decoded.Info)
	}
}

// TestInfo_KeyBundleIsWhereTheNodeReadsIt checks the encoding the node's multi-chain keyring decodes to pick the key
// bundle a report is signed with.
func TestInfo_KeyBundleIsWhereTheNodeReadsIt(t *testing.T) {
	encoded, err := encodeInfo(failedInfo)
	require.NoError(t, err)

	s := &structpb.Struct{}
	require.NoError(t, proto.Unmarshal(encoded, s))
	assert.Equal(t, "aptos", s.AsMap()["keyBundleName"])
}

func TestReportingPlugin_EncodesAndDecodesInfo(t *testing.T) {
	inner := &fakePlugin{reports: []ocr3types.ReportPlus[plugin.ReportInfo]{{
		ReportWithInfo: ocr3types.ReportWithInfo[plugin.ReportInfo]{Report: []byte("report"), Info: failedInfo},
	}}}

	factory := pluginFactory{factory: fakePluginFactory{plugin: inner}}
	p, _, err := factory.NewReportingPlugin(t.Context(), ocr3types.ReportingPluginConfig{})
	require.NoError(t, err)

	reports, err := p.Reports(t.Context(), 1, nil)
	require.NoError(t, err)
	require.Len(t, reports, 1)

	accept, err := p.ShouldAcceptAttestedReport(t.Context(), 1, reports[0].ReportWithInfo)
	require.NoError(t, err)
	assert.True(t, accept)
	assert.Equal(t, failedInfo, inner.accepted.Info)

	transmit, err := p.ShouldTransmitAcceptedReport(t.Context(), 1, reports[0].ReportWithInfo)
	require.NoError(t, err)
	assert.True(t, transmit)
	assert.Equal(t, failedInfo, inner.transmitted.Info)
}

func TestTransmitter_DecodesInfo(t *testing.T) {
	inner := &fakeTransmitter{}
	encoded, err := encodeInfo(failedInfo)
	require.NoError(t, err)

	tr := transmitter{transmitter: inner}
	require.NoError(t, tr.Transmit(t.Context(), ocrtypes.ConfigDigest{1}, 3, ocr3types.ReportWithInfo[[]byte]{Report: []byte("report"), Info: encoded}, nil))
	assert.Equal(t, failedInfo, inner.report.Info)

	account, err := tr.FromAccount(t.Context())
	require.NoError(t, err)
	assert.Equal(t, ocrtypes.Account("account"), account)
}

type fakePluginFactory struct {
	plugin ocr3types.ReportingPlugin[plugin.ReportInfo]
}

func (f fakePluginFactory) NewReportingPlugin(context.Context, ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[plugin.ReportInfo], ocr3types.ReportingPluginInfo, error) {
	return f.plugin, ocr3types.ReportingPluginInfo{Name: "fake"}, nil
}

// fakePlugin only implements what the shim changes; the rest is the embedded nil plugin.
type fakePlugin struct {
	ocr3types.ReportingPlugin[plugin.ReportInfo]
	reports     []ocr3types.ReportPlus[plugin.ReportInfo]
	accepted    ocr3types.ReportWithInfo[plugin.ReportInfo]
	transmitted ocr3types.ReportWithInfo[plugin.ReportInfo]
}

func (p *fakePlugin) Reports(context.Context, uint64, ocr3types.Outcome) ([]ocr3types.ReportPlus[plugin.ReportInfo], error) {
	return p.reports, nil
}

func (p *fakePlugin) ShouldAcceptAttestedReport(_ context.Context, _ uint64, r ocr3types.ReportWithInfo[plugin.ReportInfo]) (bool, error) {
	p.accepted = r
	return true, nil
}

func (p *fakePlugin) ShouldTransmitAcceptedReport(_ context.Context, _ uint64, r ocr3types.ReportWithInfo[plugin.ReportInfo]) (bool, error) {
	p.transmitted = r
	return true, nil
}

type fakeTransmitter struct {
	report ocr3types.ReportWithInfo[plugin.ReportInfo]
}

func (t *fakeTransmitter) Transmit(_ context.Context, _ ocrtypes.ConfigDigest, _ uint64, r ocr3types.ReportWithInfo[plugin.ReportInfo],
	_ []ocrtypes.AttributedOnchainSignature) error {
	t.report = r
	return nil
}

func (t *fakeTransmitter) FromAccount(context.Context) (ocrtypes.Account, error) { return "account", nil }
