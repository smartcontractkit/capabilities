package ocr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3confighelper"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"golang.org/x/crypto/curve25519"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"

	libsocr "github.com/smartcontractkit/capabilities/libs/ocr"
	"github.com/smartcontractkit/capabilities/libs/standalone/capability"
)

// This file is the OCR configuration of an embedded run: the one thing the in-process transport
// cannot replace.
//
// Everything else embedding needs it settles per instance - an identity of its own (keyring.go) over
// a transport that goes nowhere (inproc.go). A configuration is different because it is not one
// instance's to decide: it says who the DON is, and every member has to arrive at the same answer
// byte for byte or their digests differ and they never speak. What makes that possible here is that
// the members share a process, so the keys they sign with are in reach of all of them (see
// embedBundles) and the only thing left to say is how many there are.
//
// It is assembled exactly as the node's registry assembles one - libocr's own serialisation, and
// libsocr.ConfigDigest over the result - rather than being a second scheme that only embedded runs
// use: an embedded oracle should be joining a real configuration, one it merely computed itself.

const (
	// embeddedChainID and embeddedRegistryAddress stand in for the chain and contract a real
	// configuration was read from. They go into the digest, which is why they cannot simply be
	// left empty: the digest is what stops a configuration being replayed against another
	// registry, so an embedded run names itself rather than naming nothing - a digest computed
	// here is then valid only for an embedded run, and no configuration a node wrote can collide
	// with one.
	embeddedChainID         = 0
	embeddedRegistryAddress = "cre/standalone/embed"

	// embeddedConfigCount is the configuration's counter. An embedded DON's configuration never
	// changes - it follows from the instance count, which is fixed for the run - so this is the
	// first and only one.
	embeddedConfigCount = 1
)

// DefaultEmbeddedOCRConfig is the protocol an embedded run's oracles run under, and the struct the
// embed command's ocr.* settings are decoded into.
//
// It is libocr's own shared configuration rather than a summary of it, so every knob a real
// deployment has, an embedded run has too, under the name libocr gives it. Three fields are not
// settings: OracleIdentities is the run's instances, ConfigDigest is computed from the rest, and F
// defaults to the largest value the instance count allows (see EmbeddedOCRConfig).
//
// The values are brisk rather than production-safe: an embed run is watched by whoever started it,
// and a round a second is what makes it worth watching. They are still consistent with each other,
// which matters more than any one of them: DeltaProgress has to exceed what a round can take - a
// round, plus the four MaxDurations a plugin may spend in it - or the progress timer can fire on a
// leader that was doing nothing wrong and move the epoch on under it.
//
// Package-level because every instance runs the same protocol - they are one DON - and because what
// changes it is a flag rather than a caller.
var DefaultEmbeddedOCRConfig = ocr3confighelper.PublicConfig{
	DeltaProgress:               5 * time.Second,
	DeltaResend:                 2 * time.Second,
	DeltaInitial:                500 * time.Millisecond,
	DeltaRound:                  time.Second,
	DeltaGrace:                  500 * time.Millisecond,
	DeltaCertifiedCommitRequest: 500 * time.Millisecond,
	DeltaStage:                  5 * time.Second,
	// Rounds per epoch, after which the leader rotates. Rotation is normal - libocr says so with
	// "epoch has been going on for too long" - so this is only how much of it a reader of the log
	// sees: low enough that rotation happens while someone is watching, high enough that most of
	// what they see is rounds.
	RMax: 20,

	// What a plugin may spend in one round. Generous for a capability whose observation is a batch of
	// pending requests held in memory, and together well inside DeltaProgress.
	MaxDurationQuery:                        300 * time.Millisecond,
	MaxDurationObservation:                  300 * time.Millisecond,
	MaxDurationShouldAcceptAttestedReport:   300 * time.Millisecond,
	MaxDurationShouldTransmitAcceptedReport: 300 * time.Millisecond,
}

// init hands the builder below to the capability dependency, which is where an OCR-based capability
// finds its configuration however the run was started.
func init() {
	capability.RegisterEmbeddedOCRConfig(embeddedOCRConfigRegistry)
}

// embeddedOCRConfigRegistry returns the core.OCRConfigRegistry an embedded run's oracles read their
// configuration from.
//
// It is a registry in the sense that matters to an oracle - it answers what configuration a
// capability runs under, digest included - while holding no snapshot and reading no contract: there
// is no node behind an embedded run to read one. What it answers with is the DON of oracles
// EmbeddedOCRConfig describes.
//
// oracles is how many instances the run has, which is the oracle set: instance i of that many is the
// i-th member. It cannot be discovered from here, which is why it is asked for - see the embedded
// capability dependency, which takes it as a setting.
func embeddedOCRConfigRegistry(oracles int) core.OCRConfigRegistry {
	return embeddedRegistry{oracles: oracles}
}

// embeddedRegistry answers with the configuration of the embedded DON, whatever is asked about: the
// capability ID, DON ID and key are what the digest is computed over rather than what a record is
// looked up by, since an embedded run holds no records.
type embeddedRegistry struct {
	oracles int
}

var _ core.OCRConfigRegistry = embeddedRegistry{}

func (r embeddedRegistry) OCRConfig(_ context.Context, capabilityID string, donID uint32, key string) (ocrtypes.ContractConfig, error) {
	return EmbeddedOCRConfig(capabilityID, donID, key, r.oracles)
}

// EmbeddedOCRConfig is the OCR3 configuration a DON of oracles embedded instances runs under, as
// every one of them computes it.
//
// The same call in the same process always answers the same thing, which is what the instances rely
// on: their oracle set is this process's instances, so the configuration is over this process's keys
// (embedBundles) and is stable for as long as they are. Another process, or another run, gets a
// configuration of its own - there is nothing an embedded run outlives.
//
// Exported so a test can ask what configuration the instances it starts will run under, and check
// what it sees against it.
//
// F is the largest fault tolerance the oracle count allows (3F < N), which is what a DON of this
// size would be configured with. One instance therefore runs at F=0, which is the honest answer for
// a DON that cannot tolerate a fault rather than a refusal to run.
func EmbeddedOCRConfig(capabilityID string, donID uint32, key string, oracles int) (ocrtypes.ContractConfig, error) {
	if oracles < 1 {
		return ocrtypes.ContractConfig{}, fmt.Errorf("cannot build an OCR config for %d oracles: at least one is required", oracles)
	}
	// A capability normally runs one OCR instance, and naming it is a detail only a capability
	// running several has to care about - the same default the node's registry applies, so that a
	// capability asking for its only instance gets the same answer either way.
	if key == "" {
		key = capabilitiespb.OCR3ConfigDefaultKey
	}

	identities, err := embeddedIdentities(oracles)
	if err != nil {
		return ocrtypes.ContractConfig{}, err
	}

	cfg, err := embeddedOCR3Config(DefaultEmbeddedOCRConfig, identities)
	if err != nil {
		return ocrtypes.ContractConfig{}, err
	}

	// The same digest function the node's registry uses, over a chain and address that name this
	// run: what an oracle checks is that every member agrees, and computing it the one way is what
	// makes the embedded path exercise the real one.
	digest, err := libsocr.ConfigDigest(embeddedChainID, embeddedRegistryAddress, capabilityID, donID, key, cfg)
	if err != nil {
		return ocrtypes.ContractConfig{}, fmt.Errorf("failed to compute the OCR config digest for embedded capability %s: %w", capabilityID, err)
	}
	return capabilitiespb.OCR3ConfigFromProto(cfg, digest)
}

// embeddedIdentities is the DON, in instance order: instance i is oracle i, listed under the same
// peer ID, keys and account instance i resolves for itself when it starts.
func embeddedIdentities(oracles int) ([]confighelper.OracleIdentityExtra, error) {
	identities := make([]confighelper.OracleIdentityExtra, 0, oracles)
	for i := range oracles {
		peerID, err := DeterministicPeerID(i)
		if err != nil {
			return nil, err
		}
		bundle, err := EmbeddedOCR2Bundle(i)
		if err != nil {
			return nil, err
		}
		// The multichain form, which is what a capability DON's configuration lists members by and
		// therefore what libocr compares an oracle's own keyring against - see
		// ocr2key.NewOCR3Keyring, which is what an embedded instance signs with.
		onchainPublicKey, err := marshalEVMOnchainPublicKey(bundle.PublicKey())
		if err != nil {
			return nil, fmt.Errorf("failed to encode the onchain public key of instance %d: %w", i, err)
		}

		identities = append(identities, confighelper.OracleIdentityExtra{
			OracleIdentity: confighelper.OracleIdentity{
				OffchainPublicKey: bundle.OffchainPublicKey(),
				OnchainPublicKey:  onchainPublicKey,
				PeerID:            peerID.String(),
				TransmitAccount:   EmbeddedTransmitAccount(bundle),
			},
			ConfigEncryptionPublicKey: bundle.ConfigEncryptionPublicKey(),
		})
	}
	return identities, nil
}

// EmbeddedTransmitAccount is the account an embedded instance transmits as: its own onchain signing
// key, hex encoded, which is the form a registry's transmitters are read as.
//
// An account is part of the identity a configuration lists, and libocr checks it like the rest: an
// oracle whose account does not match its entry is not recognised as a member at all. So an embedded
// instance has to have one, it has to be its own, and it has to be something the configuration can
// name without being told - which the key it already derives is.
func EmbeddedTransmitAccount(bundle ocr2key.KeyBundle) ocrtypes.Account {
	return ocrtypes.Account(hex.EncodeToString(bundle.PublicKey()))
}

// embeddedOCR3Config assembles the configuration in the form the registry stores it, which is what
// the digest is computed over. The offchain half - the deltas, the peer IDs, the offchain public
// keys and the shared secret encrypted to each member - is serialised by libocr itself, so an
// embedded configuration is the same bytes a real one would be.
func embeddedOCR3Config(public ocr3confighelper.PublicConfig, identities []confighelper.OracleIdentityExtra) (*capabilitiespb.OCR3Config, error) {
	schedule := public.S
	if len(schedule) == 0 {
		// One stage holding every oracle, so any of them may transmit. An embedded run has nowhere to
		// transmit to but the caller in its own process, so there is nothing a staggered schedule
		// would spare.
		schedule = []int{len(identities)}
	}

	// The two fields of the real configuration an embedded run fills in itself. Offered as flags
	// because this is the real struct, and refused rather than ignored: a setting that is silently
	// overwritten is worse than one that is missing.
	if len(public.OracleIdentities) > 0 {
		return nil, errors.New("who the members of an embedded DON are follows from --instances, so --ocr.oracle-identities cannot be set")
	}
	if public.ConfigDigest != (ocrtypes.ConfigDigest{}) {
		return nil, errors.New("an embedded run computes its own config digest, so --ocr.config-digest cannot be set")
	}

	f, err := embeddedF(public.F, len(identities))
	if err != nil {
		return nil, err
	}

	signers, _, fOut, onchainConfig, offchainConfigVersion, offchainConfig, err := ocr3confighelper.ContractSetConfigArgsDeterministic(
		embeddedEphemeralSecretKey(),
		embeddedSharedSecret(),

		public.DeltaProgress,
		public.DeltaResend,
		public.DeltaInitial,
		public.DeltaRound,
		public.DeltaGrace,
		public.DeltaCertifiedCommitRequest,
		public.DeltaStage,
		public.RMax,
		schedule,
		identities,
		public.ReportingPluginConfig,
		public.MaxDurationInitialization,
		public.MaxDurationQuery,
		public.MaxDurationObservation,
		public.MaxDurationShouldAcceptAttestedReport,
		public.MaxDurationShouldTransmitAcceptedReport,
		f,
		public.OnchainConfig,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build the embedded OCR config: %w", err)
	}
	f_ := fOut

	cfg := &capabilitiespb.OCR3Config{
		F:                     uint32(f_),
		OnchainConfig:         onchainConfig,
		OffchainConfigVersion: offchainConfigVersion,
		OffchainConfig:        offchainConfig,
		ConfigCount:           embeddedConfigCount,
	}
	for i, signer := range signers {
		cfg.Signers = append(cfg.Signers, signer)
		// The registry stores an account as the bytes it hex encodes to, which is the encoding
		// EmbeddedTransmitAccount produces and the one OCR3ConfigFromProto reverses - so the
		// account an oracle reports and the one the configuration lists are the same string.
		account, derr := hex.DecodeString(string(identities[i].TransmitAccount))
		if derr != nil {
			return nil, fmt.Errorf("failed to decode the transmit account of oracle %d: %w", i, derr)
		}
		cfg.Transmitters = append(cfg.Transmitters, account)
	}
	return cfg, nil
}

// embeddedF is the fault tolerance the run is configured with.
//
// Unset - which is to say zero, since a DON of one tolerates nothing anyway - means the largest value
// the oracle count allows, so a run of four is a run that survives one bad member without anyone
// having to work out that 3F < N. A value that was asked for is checked rather than corrected: an
// oracle set that cannot support the F it was given is a configuration libocr would reject later, and
// later is a worse place to hear it.
func embeddedF(f, n int) (int, error) {
	if f == 0 {
		return (n - 1) / 3, nil
	}
	if f < 0 || 3*f >= n {
		return 0, fmt.Errorf("F of %d is not possible for %d oracles: libocr requires a non-negative F with 3F < N", f, n)
	}
	return f, nil
}

// embeddedSharedSecret is the secret an embedded DON's members derive their leaders and transmitters
// from, and embeddedEphemeralSecretKey the key it is encrypted to each of them under.
//
// Both are constants, hashed from a label. libocr calls the shared secret a low-value secret -
// knowing it says who will lead a round early, and nothing else - and an embedded run's keys are all
// public by construction anyway. What they must be is identical across instances, since they are
// part of the configuration and therefore of its digest, and a random one per instance would leave
// every instance with a DON of one.

func embeddedSharedSecret() [16]byte {
	// Truncated to the 128-bit key libocr's shared secret is.
	hash := sha256.Sum256([]byte("cre/standalone/embed/ocr3/shared-secret"))
	var secret [16]byte
	copy(secret[:], hash[:])
	return secret
}

func embeddedEphemeralSecretKey() [curve25519.ScalarSize]byte {
	return sha256.Sum256([]byte("cre/standalone/embed/ocr3/ephemeral-key"))
}
