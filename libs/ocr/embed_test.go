package ocr

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/curve25519"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3shims"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
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

// TestRemoteOraclesReachConsensus serves each instance's in-process networking the way a node serves its peer, so the
// oracles run over the network client.
func TestRemoteOraclesReachConsensus(t *testing.T) {
	t.Parallel()
	inproc := newNetwork()
	testOraclesReachConsensus(t, func(id int) ocrtypes.BinaryNetworkEndpointFactory {
		network, err := embeddedNetwork(inproc, id)
		require.NoError(t, err)

		lis := bufconn.Listen(1024 * 1024)
		s := grpc.NewServer(grpc.MaxRecvMsgSize(MaxMessageSize))
		require.NoError(t, RegisterNetworkServer(s, logger.Test(t), func(context.Context) (ocrtypes.BinaryNetworkEndpointFactory, error) {
			return network.BinaryNetworkEndpointFactory, nil
		}))
		go func() { _ = s.Serve(lis) }()
		t.Cleanup(s.Stop)

		conn, err := grpc.NewClient("passthrough:///bufnet",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		)
		require.NoError(t, err)
		t.Cleanup(func() { _ = conn.Close() })
		return newNetworkClient(logger.Test(t), conn, network.BinaryNetworkEndpointFactory.PeerID())
	})
}

func TestEmbeddedPeerIDsDiffer(t *testing.T) {
	t.Parallel()
	first, err := EmbeddedPeerID(0)
	require.NoError(t, err)
	again, err := EmbeddedPeerID(0)
	require.NoError(t, err)
	second, err := EmbeddedPeerID(1)
	require.NoError(t, err)

	assert.Equal(t, first, again, "an instance's peer is derived from its index")
	assert.NotEqual(t, first, second)
}

// testOraclesReachConsensus runs a DON of testInstances oracles, each over the networking newFactory returns, and
// checks every one transmits an attested report.
func testOraclesReachConsensus(t *testing.T, newFactory func(id int) ocrtypes.BinaryNetworkEndpointFactory) {
	config := testConfig(t)
	transmitted := make(chan transmission, 100)
	for id := range testInstances {
		lggr := logger.Test(t)
		k := testKeysOf(t, id)
		oracle, err := libocr.NewOracle(libocr.OCR3OracleArgs2[string]{
			BinaryNetworkEndpointFactory: newFactory(id),
			ContractConfigTracker:        staticConfig{config: config},
			OffchainConfigDigester:       staticConfig{config: config},
			OffchainKeyring:              k,
			OnchainKeyring:               ocr3shims.OnchainKeyringAsOnchainKeyring2[string](k),
			LocalConfig:                  testLocalConfig(),
			ReportingPluginFactory:       echoPluginFactory{},
			ContractTransmitter:          &recordingTransmitter{id: id, account: k.transmitAccount(), transmitted: transmitted},
			Database:                     NewMemoryDB(fmt.Sprint(id), lggr),
			Logger:                       logger.NewOCRWrapper(lggr, true, func(string) {}),
			MetricsRegisterer:            prometheus.NewRegistry(),
		})
		require.NoError(t, err)
		require.NoError(t, oracle.Start())
		t.Cleanup(func() { assert.NoError(t, oracle.Close()) })
	}

	seen := map[int]bool{}
	timeout := time.After(30 * time.Second)
	for len(seen) < testInstances {
		select {
		case tr := <-transmitted:
			assert.Equal(t, []byte("report"), tr.report)
			assert.GreaterOrEqual(t, tr.signatures, 2, "a report is attested by more than f oracles")
			seen[tr.id] = true
		case <-timeout:
			require.FailNow(t, "timed out waiting for every instance to transmit", "transmitted: %v", seen)
		}
	}
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

// testConfig is the config of a DON of the first testInstances embedded instances, with keys derived as their peers
// are.
func testConfig(t *testing.T) ocrtypes.ContractConfig {
	t.Helper()
	identities := make([]confighelper.OracleIdentityExtra, testInstances)
	for i := range identities {
		k := testKeysOf(t, i)
		identities[i] = confighelper.OracleIdentityExtra{
			OffchainPublicKey:         k.OffchainPublicKey(),
			OnchainPublicKey:          k.PublicKey(),
			PeerID:                    k.peerID,
			TransmitAccount:           k.transmitAccount(),
			ConfigEncryptionPublicKey: k.ConfigEncryptionPublicKey(),
		}
	}

	sharedSecret := seed("shared-secret", 0)
	signers, transmitters, f, onchainConfig, offchainConfigVersion, offchainConfig, err := ocr3confighelper.ContractSetConfigArgsDeterministic(
		sha256.Sum256([]byte(seedPrefix+"ephemeral")), [16]byte(sharedSecret[:16]),
		5*time.Second,        // DeltaProgress
		2*time.Second,        // DeltaResend
		500*time.Millisecond, // DeltaInitial
		time.Second,          // DeltaRound
		500*time.Millisecond, // DeltaGrace
		500*time.Millisecond, // DeltaCertifiedCommitRequest
		5*time.Second,        // DeltaStage
		20,                   // RMax
		[]int{testInstances},
		identities,
		nil,
		nil,
		300*time.Millisecond, // MaxDurationQuery
		300*time.Millisecond, // MaxDurationObservation
		300*time.Millisecond, // MaxDurationShouldAcceptAttestedReport
		300*time.Millisecond, // MaxDurationShouldTransmitAcceptedReport
		(testInstances-1)/3,
		nil,
	)
	require.NoError(t, err)

	config := ocrtypes.ContractConfig{
		ConfigCount:           1,
		Signers:               signers,
		Transmitters:          transmitters,
		F:                     f,
		OnchainConfig:         onchainConfig,
		OffchainConfigVersion: offchainConfigVersion,
		OffchainConfig:        offchainConfig,
	}
	config.ConfigDigest = sha256.Sum256(offchainConfig)
	binary.BigEndian.PutUint16(config.ConfigDigest[:2], uint16(ocrtypes.ConfigDigestPrefixKeystoneOCR3Capability))
	return config
}

// staticConfig is a config that never changes.
type staticConfig struct {
	config ocrtypes.ContractConfig
}

func (c staticConfig) Notify() <-chan struct{} { return nil }

func (c staticConfig) LatestConfigDetails(context.Context) (uint64, ocrtypes.ConfigDigest, error) {
	return 0, c.config.ConfigDigest, nil
}

func (c staticConfig) LatestConfig(context.Context, uint64) (ocrtypes.ContractConfig, error) {
	return c.config, nil
}

func (c staticConfig) LatestBlockHeight(context.Context) (uint64, error) { return 0, nil }

func (c staticConfig) ConfigDigest(context.Context, ocrtypes.ContractConfig) (ocrtypes.ConfigDigest, error) {
	return c.config.ConfigDigest, nil
}

func (c staticConfig) ConfigDigestPrefix(context.Context) (ocrtypes.ConfigDigestPrefix, error) {
	return ocrtypes.ConfigDigestPrefixKeystoneOCR3Capability, nil
}

// testKeys are a test oracle's keys, derived from its index, under the peer embedded instances derive.
type testKeys struct {
	peerID         string
	offchain       ed25519.PrivateKey
	offchainPublic ocrtypes.OffchainPublicKey
	configSecret   [32]byte
	configPublic   ocrtypes.ConfigEncryptionPublicKey
	onchain        ed25519.PrivateKey
}

func testKeysOf(t *testing.T, i int) *testKeys {
	t.Helper()
	peerID, err := EmbeddedPeerID(i)
	require.NoError(t, err)

	offchainSeed := seed("offchain", i)
	onchainSeed := seed("onchain", i)
	k := &testKeys{
		peerID:       peerID,
		offchain:     ed25519.NewKeyFromSeed(offchainSeed[:]),
		configSecret: seed("config", i),
		onchain:      ed25519.NewKeyFromSeed(onchainSeed[:]),
	}
	configPublic, err := curve25519.X25519(k.configSecret[:], curve25519.Basepoint)
	require.NoError(t, err)
	copy(k.configPublic[:], configPublic)
	copy(k.offchainPublic[:], k.offchain.Public().(ed25519.PublicKey))
	return k
}

func (k *testKeys) transmitAccount() ocrtypes.Account {
	return ocrtypes.Account(hex.EncodeToString(k.PublicKey()))
}

func (k *testKeys) OffchainSign(msg []byte) ([]byte, error) {
	return ed25519.Sign(k.offchain, msg), nil
}

func (k *testKeys) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
	var shared [32]byte
	secret, err := curve25519.X25519(k.configSecret[:], point[:])
	if err != nil {
		return shared, err
	}

	copy(shared[:], secret)
	return shared, nil
}

func (k *testKeys) OffchainPublicKey() ocrtypes.OffchainPublicKey { return k.offchainPublic }

func (k *testKeys) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey {
	return k.configPublic
}

func (k *testKeys) PublicKey() ocrtypes.OnchainPublicKey {
	return ocrtypes.OnchainPublicKey(k.onchain.Public().(ed25519.PublicKey))
}

func (k *testKeys) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[string]) ([]byte, error) {
	return ed25519.Sign(k.onchain, reportHash(digest, seqNr, report.Report)), nil
}

func (k *testKeys) Verify(key ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[string], signature []byte) bool {
	return len(key) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(key), reportHash(digest, seqNr, report.Report), signature)
}

func (k *testKeys) MaxSignatureLength() int { return ed25519.SignatureSize }

func reportHash(digest ocrtypes.ConfigDigest, seqNr uint64, report []byte) []byte {
	h := sha256.New()
	h.Write(digest[:])
	_ = binary.Write(h, binary.BigEndian, seqNr)
	h.Write(report)
	return h.Sum(nil)
}

type transmission struct {
	id         int
	report     []byte
	signatures int
}

type recordingTransmitter struct {
	id          int
	account     ocrtypes.Account
	transmitted chan<- transmission
}

func (r *recordingTransmitter) Transmit(_ context.Context, _ ocrtypes.ConfigDigest, _ uint64,
	report ocr3types.ReportWithInfo[string], sigs []ocrtypes.AttributedOnchainSignature) error {
	select {
	case r.transmitted <- transmission{id: r.id, report: report.Report, signatures: len(sigs)}:
	default:
	}

	return nil
}

func (r *recordingTransmitter) FromAccount(context.Context) (ocrtypes.Account, error) {
	return r.account, nil
}

// echoPluginFactory makes a plugin that reports the same thing every round.
type echoPluginFactory struct{}

func (echoPluginFactory) NewReportingPlugin(context.Context, ocr3types.ReportingPluginConfig) (ocr3types.ReportingPlugin[string], ocr3types.ReportingPluginInfo, error) {
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

func (echoPlugin) Reports(context.Context, uint64, ocr3types.Outcome) ([]ocr3types.ReportPlus[string], error) {
	return []ocr3types.ReportPlus[string]{{ReportWithInfo: ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: "evm"}}}, nil
}

func (echoPlugin) ShouldAcceptAttestedReport(context.Context, uint64, ocr3types.ReportWithInfo[string]) (bool, error) {
	return true, nil
}

func (echoPlugin) ShouldTransmitAcceptedReport(context.Context, uint64, ocr3types.ReportWithInfo[string]) (bool, error) {
	return true, nil
}

func (echoPlugin) Close() error { return nil }
