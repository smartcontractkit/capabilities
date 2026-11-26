package ocr

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	"github.com/samber/do/v2"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3confighelper"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"

	"github.com/smartcontractkit/capabilities/libs/capability"
	keystoreocr "github.com/smartcontractkit/capabilities/libs/capability/keystore/ocr"
)

func newEmbeddedNetwork(i do.Injector) (Network, error) {
	info, err := do.Invoke[capability.InstanceInfo](i)
	if err != nil {
		return Network{}, err
	}

	reg, err := do.Invoke[registry.CapabilitiesRegistry](i)
	if err != nil {
		return Network{}, err
	}

	configs, ok := reg.(interface {
		AddOCR3Config(key string, config ocrtypes.ContractConfig) error
	})
	if !ok {
		return Network{}, fmt.Errorf("an embedded instance's registry %T can't be given an OCR config", reg)
	}

	config, err := embeddedConfig(info.Total)
	if err != nil {
		return Network{}, err
	}

	if err = configs.AddOCR3Config(capabilitiespb.OCR3ConfigDefaultKey, config); err != nil {
		return Network{}, err
	}

	return embeddedNetwork(embedNetwork, info.ID)
}

func embeddedNetwork(net *network, id int) (Network, error) {
	peerID, err := capability.EmbeddedPeerID(id)
	if err != nil {
		return Network{}, err
	}

	return Network{BinaryNetworkEndpointFactory: ocr2Factory{net: net, peerID: peerID.String(), bufferSize: defaultBufferSize}}, nil
}

// seedPrefix separates the keys derived here from any other keys derived from an index.
const seedPrefix = "cre/capability/embed/ocr/"

func seed(purpose string, i int) [32]byte {
	return sha256.Sum256([]byte(seedPrefix + purpose + "/" + strconv.Itoa(i)))
}

func embeddedTransmitAccount(onchainPublicKey ocrtypes.OnchainPublicKey) ocrtypes.Account {
	return ocrtypes.Account(hex.EncodeToString(onchainPublicKey))
}

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

// embeddedConfig is the config of a DON of the first oracles instances, the same for every instance.
func embeddedConfig(oracles int) (ocrtypes.ContractConfig, error) {
	identities := make([]confighelper.OracleIdentityExtra, oracles)
	for i := range identities {
		peerID, err := capability.EmbeddedPeerID(i)
		if err != nil {
			return ocrtypes.ContractConfig{}, err
		}

		bundles, err := keystoreocr.EmbeddedKeyBundles(i)
		if err != nil {
			return ocrtypes.ContractConfig{}, err
		}

		keys := ocr2key.MultichainPublicKey{}
		var bundle keystoreocr.KeyBundle
		for _, b := range bundles {
			keys[b.ChainType()] = b.PublicKey()
			// The node's own oracles sign offchain with their evm bundle.
			if b.ChainType() == string(corekeys.EVM) {
				bundle = b
			}
		}

		signer, err := keys.MarshalBinary()
		if err != nil {
			return ocrtypes.ContractConfig{}, fmt.Errorf("failed to encode the onchain public keys of instance %d: %w", i, err)
		}

		identities[i] = confighelper.OracleIdentityExtra{
			OffchainPublicKey:         bundle.OffchainPublicKey(),
			OnchainPublicKey:          signer,
			PeerID:                    peerID.String(),
			TransmitAccount:           embeddedTransmitAccount(bundle.PublicKey()),
			ConfigEncryptionPublicKey: bundle.ConfigEncryptionPublicKey(),
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
