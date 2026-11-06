package ocr

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	commonocr "github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/ocrutil"
)

func TestEmbeddedOraclesReachConsensus(t *testing.T) {
	t.Parallel()
	network := newNetwork()
	testOraclesReachConsensus(t, func(id, total int) Dependencies {
		deps, err := embeddedDependencies(network, id, total)
		require.NoError(t, err)
		return deps
	})
}

// TestRemoteOraclesReachConsensus serves each instance's peer and keys the way a node does, so the oracles run over
// the remote client.
func TestRemoteOraclesReachConsensus(t *testing.T) {
	t.Parallel()
	network := newNetwork()
	testOraclesReachConsensus(t, func(id, total int) Dependencies {
		deps, err := embeddedDependencies(network, id, total)
		require.NoError(t, err)

		node := deps.Node
		lis := bufconn.Listen(1024 * 1024)
		s := grpc.NewServer(commonocr.ServerOptions()...)
		commonocr.RegisterServer(s, logger.Test(t), func(context.Context) (commonocr.Node, error) { return node, nil })
		go func() { _ = s.Serve(lis) }()
		t.Cleanup(s.Stop)

		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })

		deps.Node, err = commonocr.Connect(t.Context(), logger.Test(t), conn, node.BinaryNetworkEndpointFactory.PeerID(),
			commonocr.Config{}, node.OnchainKeyring.Verify)
		require.NoError(t, err)
		return deps
	})
}

// testOraclesReachConsensus runs a DON of 4 embedded instances, each over the dependencies newDependencies returns.
func testOraclesReachConsensus(t *testing.T, newDependencies func(id, total int) Dependencies) {
	const instances = 4
	transmitted := make(chan transmission, 100)
	for id := range instances {
		lggr := logger.Test(t)
		oracle, err := libocr.NewOracle(WithNode("test@1.0.0", libocr.OCR3OracleArgs2[ReportInfo[string]]{
			LocalConfig:            testLocalConfig(),
			ReportingPluginFactory: echoPluginFactory{},
			ContractTransmitter:    &recordingTransmitter{id: id, transmitted: transmitted},
			Database:               ocrutil.NewMemoryDB(fmt.Sprint(id), lggr),
			Logger:                 logger.NewOCRWrapper(lggr, true, func(string) {}),
			MetricsRegisterer:      prometheus.NewRegistry(),
		}, newDependencies(id, instances)))
		require.NoError(t, err)
		require.NoError(t, oracle.Start())
		t.Cleanup(func() { assert.NoError(t, oracle.Close()) })
	}

	seen := map[int]bool{}
	timeout := time.After(30 * time.Second)
	for len(seen) < instances {
		select {
		case tr := <-transmitted:
			assert.Equal(t, []byte("report"), tr.report)
			assert.Equal(t, ReportInfo[string]{KeyBundle: "evm", Value: "value"}, tr.info)
			assert.GreaterOrEqual(t, tr.signatures, 2, "a report is attested by more than f oracles")
			seen[tr.id] = true
		case <-timeout:
			require.FailNow(t, "timed out waiting for every instance to transmit", "transmitted: %v", seen)
		}
	}
}

func TestEmbeddedConfigIsSharedByInstances(t *testing.T) {
	t.Parallel()
	first, err := embeddedDependencies(newNetwork(), 0, 4)
	require.NoError(t, err)
	second, err := embeddedDependencies(newNetwork(), 3, 4)
	require.NoError(t, err)

	firstTracker, _ := first.Config("test@1.0.0")
	firstConfig, err := firstTracker.LatestConfig(t.Context(), 0)
	require.NoError(t, err)
	secondTracker, _ := second.Config("test@1.0.0")
	secondConfig, err := secondTracker.LatestConfig(t.Context(), 0)
	require.NoError(t, err)
	assert.Equal(t, firstConfig, secondConfig)
	assert.Equal(t, firstConfig.Transmitters[3], second.TransmitAccount)
	assert.NotEqual(t, first.BinaryNetworkEndpointFactory.PeerID(), second.BinaryNetworkEndpointFactory.PeerID())

	_, err = embeddedDependencies(newNetwork(), 4, 4)
	require.Error(t, err)
}

func TestKeysSignAndVerify(t *testing.T) {
	t.Parallel()
	k, err := deriveKeys(0)
	require.NoError(t, err)
	other, err := deriveKeys(1)
	require.NoError(t, err)

	report := ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: "evm"}
	sig, err := k.Sign(ocrtypes.ConfigDigest{1}, 2, report)
	require.NoError(t, err)
	assert.True(t, other.Verify(k.PublicKey(), ocrtypes.ConfigDigest{1}, 2, report, sig))
	assert.False(t, other.Verify(k.PublicKey(), ocrtypes.ConfigDigest{1}, 3, report, sig))

	kSecret, err := k.ConfigDiffieHellman(other.ConfigEncryptionPublicKey())
	require.NoError(t, err)
	otherSecret, err := other.ConfigDiffieHellman(k.ConfigEncryptionPublicKey())
	require.NoError(t, err)
	assert.Equal(t, kSecret, otherSecret)
}

func testLocalConfig() ocrtypes.LocalConfig {
	return ocrtypes.LocalConfig{
		BlockchainTimeout:                  time.Second,
		ContractConfigTrackerPollInterval:  time.Second,
		ContractConfigConfirmations:        1,
		ContractTransmitterTransmitTimeout: time.Second,
		DatabaseTimeout:                    time.Second,
		ContractConfigLoadTimeout:          time.Second,
		DefaultMaxDurationInitialization:   time.Second,
	}
}

type transmission struct {
	id         int
	report     []byte
	info       ReportInfo[string]
	signatures int
}

type recordingTransmitter struct {
	id          int
	transmitted chan<- transmission
}

func (r *recordingTransmitter) Transmit(_ context.Context, _ ocrtypes.ConfigDigest, _ uint64,
	report ocr3types.ReportWithInfo[ReportInfo[string]], sigs []ocrtypes.AttributedOnchainSignature) error {
	select {
	case r.transmitted <- transmission{id: r.id, report: report.Report, info: report.Info, signatures: len(sigs)}:
	default:
	}

	return nil
}

func (r *recordingTransmitter) FromAccount(context.Context) (ocrtypes.Account, error) {
	return "", nil
}

// echoPluginFactory makes a plugin that reports the same thing every round.
type echoPluginFactory struct{}

func (echoPluginFactory) NewReportingPlugin(context.Context, ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[ReportInfo[string]], ocr3types.ReportingPluginInfo, error) {
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

func (echoPlugin) Reports(context.Context, uint64, ocr3types.Outcome) ([]ocr3types.ReportPlus[ReportInfo[string]], error) {
	return []ocr3types.ReportPlus[ReportInfo[string]]{{ReportWithInfo: ocr3types.ReportWithInfo[ReportInfo[string]]{
		Report: []byte("report"),
		Info:   ReportInfo[string]{KeyBundle: "evm", Value: "value"},
	}}}, nil
}

func (echoPlugin) ShouldAcceptAttestedReport(context.Context, uint64, ocr3types.ReportWithInfo[ReportInfo[string]]) (bool, error) {
	return true, nil
}

func (echoPlugin) ShouldTransmitAcceptedReport(context.Context, uint64, ocr3types.ReportWithInfo[ReportInfo[string]]) (bool, error) {
	return true, nil
}

func (echoPlugin) Close() error { return nil }
