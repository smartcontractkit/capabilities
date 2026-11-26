package ocr

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"

	keystoreocr "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

const testInstances = 4

func TestEmbeddedOraclesReachConsensus(t *testing.T) {
	t.Parallel()
	inproc := newNetwork()
	testOraclesReachConsensus(t, func(id int) ocrtypes.BinaryNetworkEndpointFactory {
		network, err := embeddedNetwork(inproc, id)
		require.NoError(t, err)
		return network.BinaryNetworkEndpointFactory
	})
}

func TestEmbeddedConfigIsSharedByInstances(t *testing.T) {
	t.Parallel()
	first, last := testIdentity(t, 0), testIdentity(t, testInstances-1)

	firstConfig, err := first.ContractConfigTracker.LatestConfig(t.Context(), 0)
	require.NoError(t, err)
	lastConfig, err := last.ContractConfigTracker.LatestConfig(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, firstConfig, lastConfig)
	assert.Equal(t, firstConfig.Signers[testInstances-1], last.onchainKeyring.PublicKey())
	assert.Equal(t, firstConfig.Transmitters[testInstances-1], last.TransmitAccount)

	for _, info := range []string{"evm", "stellar"} {
		report := ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: info}
		sig, err := last.onchainKeyring.Sign(ocrtypes.ConfigDigest{1}, 2, report)
		require.NoError(t, err, "an embedded instance signs reports for any chain")
		assert.True(t, first.onchainKeyring.Verify(last.onchainKeyring.PublicKey(), ocrtypes.ConfigDigest{1}, 2, report, sig))
	}
}

// testIdentity is instance id's identity in an embedded run of testInstances, found as NewRegistryOracle finds it.
func testIdentity(t *testing.T, id int) identity {
	t.Helper()
	identity, err := nodeIdentity(t.Context(), testEmbeddedClient(t, id), testRegistryConfig(t))
	require.NoError(t, err)
	return identity
}

func testEmbeddedClient(t *testing.T, id int) keystoreocr.Client {
	t.Helper()
	client, err := keystoreocr.NewEmbeddedClient(id)
	require.NoError(t, err)
	return client
}

// testRegistryConfig is the OCR config of an embedded run of testInstances, as an instance's registry gives it.
func testRegistryConfig(t *testing.T) *registryConfig {
	t.Helper()
	config, err := embeddedConfig(testInstances)
	require.NoError(t, err)
	return &registryConfig{registry: &fakeRegistry{configs: map[string]ocrtypes.ContractConfig{
		capabilitiespb.OCR3ConfigDefaultKey: config}}, capabilityID: "test@1.0.0"}
}

func testLocalConfig() ocrtypes.LocalConfig {
	return ocrtypes.LocalConfig{
		BlockchainTimeout:                  time.Second,
		ContractConfigConfirmations:        1,
		SkipContractConfigConfirmations:    true,
		ContractConfigTrackerPollInterval:  time.Second,
		ContractTransmitterTransmitTimeout: time.Second,
		DatabaseTimeout:                    time.Second,
		ContractConfigLoadTimeout:          time.Second,
		DefaultMaxDurationInitialization:   time.Second,
	}
}

type transmission struct {
	id         int
	report     []byte
	info       echoInfo
	signatures int
}

type recordingTransmitter struct {
	id          int
	transmitted chan<- transmission
}

func (r *recordingTransmitter) Transmit(_ context.Context, _ ocrtypes.ConfigDigest, _ uint64,
	report ocr3types.ReportWithInfo[echoInfo], sigs []ocrtypes.AttributedOnchainSignature) error {
	select {
	case r.transmitted <- transmission{id: r.id, report: report.Report, info: report.Info, signatures: len(sigs)}:
	default:
	}

	return nil
}

func (r *recordingTransmitter) FromAccount(context.Context) (ocrtypes.Account, error) {
	return "", nil
}

// echoInfo is the info of an echo plugin's report.
type echoInfo struct {
	keyBundle string
	value     string
}

func (i echoInfo) KeyBundle() string { return i.keyBundle }

// echoPluginFactory makes a plugin that reports the same thing every round.
type echoPluginFactory struct{}

func (echoPluginFactory) NewReportingPlugin(context.Context, ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[echoInfo], ocr3types.ReportingPluginInfo, error) {
	return echoPlugin{}, ocr3types.ReportingPluginInfo{
		Name: "echo",
		Limits: ocr3types.ReportingPluginLimits{
			MaxQueryLength:       10,
			MaxObservationLength: 10,
			MaxOutcomeLength:     10,
			MaxReportLength:      10,
			MaxReportCount:       1,
		},
	}, nil
}

type echoPlugin struct{}

func (echoPlugin) Query(context.Context, ocr3types.OutcomeContext) (ocrtypes.Query, error) {
	return nil, nil
}

func (echoPlugin) Observation(context.Context, ocr3types.OutcomeContext, ocrtypes.Query) (ocrtypes.Observation, error) {
	return ocrtypes.Observation("obs"), nil
}

func (echoPlugin) ValidateObservation(context.Context, ocr3types.OutcomeContext, ocrtypes.Query, ocrtypes.AttributedObservation) error {
	return nil
}

func (echoPlugin) ObservationQuorum(_ context.Context, _ ocr3types.OutcomeContext, _ ocrtypes.Query, aos []ocrtypes.AttributedObservation) (bool, error) {
	return len(aos) >= 2, nil
}

func (echoPlugin) Outcome(context.Context, ocr3types.OutcomeContext, ocrtypes.Query, []ocrtypes.AttributedObservation) (ocr3types.Outcome, error) {
	return ocr3types.Outcome("outcome"), nil
}

func (echoPlugin) Reports(context.Context, uint64, ocr3types.Outcome) ([]ocr3types.ReportPlus[echoInfo], error) {
	return []ocr3types.ReportPlus[echoInfo]{{ReportWithInfo: ocr3types.ReportWithInfo[echoInfo]{
		Report: []byte("report"),
		Info:   echoInfo{keyBundle: "evm", value: "value"},
	}}}, nil
}

func (echoPlugin) ShouldAcceptAttestedReport(context.Context, uint64, ocr3types.ReportWithInfo[echoInfo]) (bool, error) {
	return true, nil
}

func (echoPlugin) ShouldTransmitAcceptedReport(context.Context, uint64, ocr3types.ReportWithInfo[echoInfo]) (bool, error) {
	return true, nil
}

func (echoPlugin) Close() error { return nil }
