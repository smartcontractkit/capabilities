package evmcontracts

import (
	"encoding/hex"
	"fmt"
	"time"

	"github.com/smartcontractkit/libocr/offchainreporting2/confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2/types"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3confighelper"

	"github.com/smartcontractkit/capabilities/libs/cli/node"
)

type OCR2Config struct {
	Signers               []types.OnchainPublicKey
	Transmitters          []types.Account
	F                     uint8
	OnchainConfig         []byte
	OffchainConfigVersion uint64
	OffchainConfig        []byte
}

func generateOCR3Config(nodeIDs []int) (*OCR2Config, error) {
	oracleIdentities := []confighelper.OracleIdentityExtra{}
	for _, nodeID := range nodeIDs {
		publicKeys, err := node.GetPublicKeys(nodeID)
		if err != nil {
			return nil, fmt.Errorf("failed to get public keys for node %d: %w", nodeID, err)
		}

		onchainPubkeyBytes, err := hex.DecodeString(publicKeys.OCR2OnchainPubkey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode OCR2OnchainPubkey: %w", err)
		}

		offchainPublicKeyBytes, err := hex.DecodeString(publicKeys.OCR2OffchainPubkey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode OCR2OffchainPubkey: %w", err)
		}
		var offchainPublicKey [32]byte
		copy(offchainPublicKey[:], offchainPublicKeyBytes)

		sharedSecretEncryptionPublicKeyBytes, err := hex.DecodeString(publicKeys.OCR2ConfigPubkey)
		if err != nil {
			return nil, fmt.Errorf("failed to decode OCR2ConfigPubkey: %w", err)
		}
		var sharedSecretEncryptionPublicKey [32]byte
		copy(sharedSecretEncryptionPublicKey[:], sharedSecretEncryptionPublicKeyBytes)

		oracleIdentities = append(oracleIdentities, confighelper.OracleIdentityExtra{
			OracleIdentity: confighelper.OracleIdentity{
				PeerID:            publicKeys.P2PPeerID,
				OnchainPublicKey:  onchainPubkeyBytes,
				OffchainPublicKey: offchainPublicKey,
				TransmitAccount:   types.Account(publicKeys.EthAddress),
			},
			ConfigEncryptionPublicKey: sharedSecretEncryptionPublicKey,
		})
	}

	// Generate OCR3 configuration arguments for testing
	signers, transmitters, f, onchainConfig, offchainConfigVersion, offchainConfig, err := ocr3confighelper.ContractSetConfigArgsForTests(
		5*time.Second,        // DeltaProgress: Time between rounds
		5*time.Second,        // DeltaResend: Time between resending unconfirmed transmissions
		5*time.Second,        // DeltaInitial: Initial delay before starting the first round
		2*time.Second,        // DeltaRound: Time between rounds within an epoch
		500*time.Millisecond, // DeltaGrace: Grace period for delayed transmissions
		1*time.Second,        // DeltaCertifiedCommitRequest: Time between certified commit requests
		30*time.Second,       // DeltaStage: Time between stages of the protocol
		10,                   // MaxRoundsPerEpoch: Maximum number of rounds per epoch
		nodeIDs,              // TransmissionSchedule: Transmission schedule
		oracleIdentities,     // Oracle identities with their public keys
		nil,                  // Plugin config (empty for now)
		1*time.Second,        // MaxDurationQuery: Maximum duration for querying
		1*time.Second,        // MaxDurationObservation: Maximum duration for observation
		1*time.Second,        // MaxDurationAccept: Maximum duration for acceptance
		1*time.Second,        // MaxDurationTransmit: Maximum duration for transmission
		1,                    // F: Maximum number of faulty oracles
		nil,                  // OnChain config (empty for now)
	)
	if err != nil {
		return nil, fmt.Errorf("failed to set config args: %w", err)
	}

	return &OCR2Config{
		Signers:               signers,
		Transmitters:          transmitters,
		F:                     f,
		OnchainConfig:         onchainConfig,
		OffchainConfigVersion: offchainConfigVersion,
		OffchainConfig:        offchainConfig,
	}, nil
}
