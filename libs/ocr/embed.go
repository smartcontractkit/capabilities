package ocr

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	ragetypes "github.com/smartcontractkit/libocr/ragep2p/types"

	commonkeyring "github.com/smartcontractkit/chainlink-common/pkg/capabilities/ocr/keyring"
)

// An embedded run's instances are a DON on their own: each derives its keys from its index, they talk over the
// in-process network, and their config is computed from how many there are. Everything here is public by
// construction, so embed is for local runs and tests.

// embeddedPublicConfig is what the oracles of an embedded run run under. It is brisk, so a run makes progress while
// someone watches it, and consistent: DeltaProgress exceeds a round and the durations a plugin may spend in it.
var embeddedPublicConfig = ocr3confighelper.PublicConfig{
	DeltaProgress:                           5 * time.Second,
	DeltaResend:                             2 * time.Second,
	DeltaInitial:                            500 * time.Millisecond,
	DeltaRound:                              time.Second,
	DeltaGrace:                              500 * time.Millisecond,
	DeltaCertifiedCommitRequest:             500 * time.Millisecond,
	DeltaStage:                              5 * time.Second,
	RMax:                                    20,
	MaxDurationQuery:                        300 * time.Millisecond,
	MaxDurationObservation:                  300 * time.Millisecond,
	MaxDurationShouldAcceptAttestedReport:   300 * time.Millisecond,
	MaxDurationShouldTransmitAcceptedReport: 300 * time.Millisecond,
}

// EmbeddedDON is the OCR dependencies of each of n instances of a DON over a network of their own in this process,
// as embed gives them.
func EmbeddedDON(n int) ([]Dependencies, error) {
	net := newNetwork()
	deps := make([]Dependencies, n)
	for i := range deps {
		d, err := embeddedDependencies(net, i, n)
		if err != nil {
			return nil, err
		}

		deps[i] = d
	}

	return deps, nil
}

// embeddedDependencies are those of instance id of an embedded run of total instances, talking over net.
func embeddedDependencies(net *network, id, total int) (Dependencies, error) {
	if id < 0 || id >= total {
		return Dependencies{}, fmt.Errorf("instance %d is not one of the %d instances of this run", id, total)
	}

	keys, err := deriveKeys(id)
	if err != nil {
		return Dependencies{}, err
	}

	config, err := embeddedConfig(total)
	if err != nil {
		return Dependencies{}, err
	}

	return Dependencies{
		BinaryNetworkEndpointFactory: ocr2Factory{net: net, peerID: keys.peerID, bufferSize: defaultBufferSize},
		Keyrings:                     commonkeyring.Keyrings{OffchainKeyring: keys, OnchainKeyring: keys},
		TransmitAccount:              keys.transmitAccount(),
		Config: func(string) (ocrtypes.ContractConfigTracker, ocrtypes.OffchainConfigDigester) {
			return staticConfig{config: config}, staticConfig{config: config}
		},
	}, nil
}

// embeddedConfig is the config of a DON of the first oracles instances, the same for every instance.
func embeddedConfig(oracles int) (ocrtypes.ContractConfig, error) {
	identities := make([]confighelper.OracleIdentityExtra, oracles)
	for i := range identities {
		keys, err := deriveKeys(i)
		if err != nil {
			return ocrtypes.ContractConfig{}, err
		}

		identities[i] = confighelper.OracleIdentityExtra{
			OffchainPublicKey:         keys.OffchainPublicKey(),
			OnchainPublicKey:          keys.PublicKey(),
			PeerID:                    keys.peerID,
			TransmitAccount:           keys.transmitAccount(),
			ConfigEncryptionPublicKey: keys.ConfigEncryptionPublicKey(),
		}
	}

	pub := embeddedPublicConfig
	sharedSecret := seed("shared-secret", 0)
	signers, transmitters, f, onchainConfig, offchainConfigVersion, offchainConfig, err := ocr3confighelper.ContractSetConfigArgsDeterministic(
		sha256.Sum256([]byte(seedPrefix+"ephemeral")), [16]byte(sharedSecret[:16]),
		pub.DeltaProgress,
		pub.DeltaResend,
		pub.DeltaInitial,
		pub.DeltaRound,
		pub.DeltaGrace,
		pub.DeltaCertifiedCommitRequest,
		pub.DeltaStage,
		pub.RMax,
		[]int{oracles},
		identities,
		nil,
		nil,
		pub.MaxDurationQuery,
		pub.MaxDurationObservation,
		pub.MaxDurationShouldAcceptAttestedReport,
		pub.MaxDurationShouldTransmitAcceptedReport,
		(oracles-1)/3,
		nil,
	)
	if err != nil {
		return ocrtypes.ContractConfig{}, fmt.Errorf("failed to build the embedded OCR config: %w", err)
	}

	config := ocrtypes.ContractConfig{
		ConfigCount:           1,
		Signers:               signers,
		Transmitters:          transmitters,
		F:                     f,
		OnchainConfig:         onchainConfig,
		OffchainConfigVersion: offchainConfigVersion,
		OffchainConfig:        offchainConfig,
	}
	config.ConfigDigest = embeddedDigest(config)
	return config, nil
}

// embeddedDigest only has to be the same for every instance of the run, which share a process.
func embeddedDigest(cc ocrtypes.ContractConfig) ocrtypes.ConfigDigest {
	h := sha256.New()
	_ = binary.Write(h, binary.BigEndian, cc.ConfigCount)
	for _, s := range cc.Signers {
		_ = binary.Write(h, binary.BigEndian, uint32(len(s))) //nolint:gosec // keys are small
		h.Write(s)
	}

	for _, t := range cc.Transmitters {
		_ = binary.Write(h, binary.BigEndian, uint32(len(t))) //nolint:gosec // accounts are small
		h.Write([]byte(t))
	}

	h.Write([]byte{cc.F})
	h.Write(cc.OnchainConfig)
	_ = binary.Write(h, binary.BigEndian, cc.OffchainConfigVersion)
	h.Write(cc.OffchainConfig)

	var digest ocrtypes.ConfigDigest
	copy(digest[:], h.Sum(nil))
	binary.BigEndian.PutUint16(digest[:2], uint16(ocrtypes.ConfigDigestPrefixKeystoneOCR3Capability))
	return digest
}

// staticConfig is the config of an embedded run, which never changes.
type staticConfig struct {
	config ocrtypes.ContractConfig
}

var (
	_ ocrtypes.ContractConfigTracker  = staticConfig{}
	_ ocrtypes.OffchainConfigDigester = staticConfig{}
)

func (c staticConfig) Notify() <-chan struct{} { return nil }

func (c staticConfig) LatestConfigDetails(context.Context) (uint64, ocrtypes.ConfigDigest, error) {
	return 0, c.config.ConfigDigest, nil
}

func (c staticConfig) LatestConfig(context.Context, uint64) (ocrtypes.ContractConfig, error) {
	return c.config, nil
}

func (c staticConfig) LatestBlockHeight(context.Context) (uint64, error) { return 0, nil }

func (c staticConfig) ConfigDigest(_ context.Context, cc ocrtypes.ContractConfig) (ocrtypes.ConfigDigest, error) {
	return embeddedDigest(cc), nil
}

func (c staticConfig) ConfigDigestPrefix(context.Context) (ocrtypes.ConfigDigestPrefix, error) {
	return ocrtypes.ConfigDigestPrefixKeystoneOCR3Capability, nil
}

// seedPrefix separates the keys derived here from any other keys derived from an index.
const seedPrefix = "cre/capability/embed/ocr/"

func seed(purpose string, i int) [32]byte {
	return sha256.Sum256([]byte(seedPrefix + purpose + "/" + strconv.Itoa(i)))
}

// keys are an embedded instance's peer and OCR keys, derived from its index.
type keys struct {
	peerID         string
	offchain       ed25519.PrivateKey
	configSecret   [32]byte
	configPublic   ocrtypes.ConfigEncryptionPublicKey
	onchain        ed25519.PrivateKey
	onchainPublic  ocrtypes.OnchainPublicKey
	offchainPublic ocrtypes.OffchainPublicKey
}

var (
	_ ocrtypes.OffchainKeyring         = (*keys)(nil)
	_ ocr3types.OnchainKeyring[string] = (*keys)(nil)
)

func deriveKeys(i int) (*keys, error) {
	peerSeed := seed("peer", i)
	peerID, err := ragetypes.PeerIDFromPublicKey(ed25519.NewKeyFromSeed(peerSeed[:]).Public().(ed25519.PublicKey))
	if err != nil {
		return nil, fmt.Errorf("failed to derive the peer ID of instance %d: %w", i, err)
	}

	offchainSeed := seed("offchain", i)
	onchainSeed := seed("onchain", i)
	k := &keys{
		peerID:       peerID.String(),
		offchain:     ed25519.NewKeyFromSeed(offchainSeed[:]),
		configSecret: seed("config", i),
		onchain:      ed25519.NewKeyFromSeed(onchainSeed[:]),
	}

	configPublic, err := curve25519.X25519(k.configSecret[:], curve25519.Basepoint)
	if err != nil {
		return nil, fmt.Errorf("failed to derive the config encryption key of instance %d: %w", i, err)
	}

	copy(k.configPublic[:], configPublic)
	copy(k.offchainPublic[:], k.offchain.Public().(ed25519.PublicKey))
	k.onchainPublic = ocrtypes.OnchainPublicKey(k.onchain.Public().(ed25519.PublicKey))
	return k, nil
}

func (k *keys) transmitAccount() ocrtypes.Account {
	return ocrtypes.Account(hex.EncodeToString(k.onchainPublic))
}

func (k *keys) OffchainSign(msg []byte) ([]byte, error) { return ed25519.Sign(k.offchain, msg), nil }

func (k *keys) ConfigDiffieHellman(point [32]byte) ([32]byte, error) {
	var shared [32]byte
	secret, err := curve25519.X25519(k.configSecret[:], point[:])
	if err != nil {
		return shared, err
	}

	copy(shared[:], secret)
	return shared, nil
}

func (k *keys) OffchainPublicKey() ocrtypes.OffchainPublicKey { return k.offchainPublic }

func (k *keys) ConfigEncryptionPublicKey() ocrtypes.ConfigEncryptionPublicKey { return k.configPublic }

func (k *keys) PublicKey() ocrtypes.OnchainPublicKey { return k.onchainPublic }

func (k *keys) Sign(digest ocrtypes.ConfigDigest, seqNr uint64, report ocr3types.ReportWithInfo[string]) ([]byte, error) {
	return ed25519.Sign(k.onchain, reportHash(digest, seqNr, report.Report)), nil
}

func (k *keys) Verify(key ocrtypes.OnchainPublicKey, digest ocrtypes.ConfigDigest, seqNr uint64,
	report ocr3types.ReportWithInfo[string], signature []byte) bool {
	return len(key) == ed25519.PublicKeySize && ed25519.Verify(ed25519.PublicKey(key), reportHash(digest, seqNr, report.Report), signature)
}

func (k *keys) MaxSignatureLength() int { return ed25519.SignatureSize }

func reportHash(digest ocrtypes.ConfigDigest, seqNr uint64, report []byte) []byte {
	h := sha256.New()
	h.Write(digest[:])
	_ = binary.Write(h, binary.BigEndian, seqNr)
	h.Write(report)
	return h.Sum(nil)
}
