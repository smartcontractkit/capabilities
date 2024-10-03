package oracle

import (
	"context"
	"fmt"
	"time"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/confighelper"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3confighelper"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ types.ContractConfigTracker = (*contractConfigTracker)(nil)

type contractConfigTracker struct {
	logger logger.Logger
	config types.ContractConfig
}

func NewContractConfigTracker(logger logger.Logger, oracleIdentity Identity) (*contractConfigTracker, error) {
	// Testnet config
	// {
	// 	"MaxQueryLengthBytes": 1000000,
	// 	"MaxObservationLengthBytes": 1000000,
	// 	"MaxReportLengthBytes": 1000000,
	// 	"MaxRequestBatchSize": 1000,
	// 	"UniqueReports": true,
	// 	"DeltaProgressMillis": 5000,
	// 	"DeltaResendMillis": 5000,
	// 	"DeltaInitialMillis": 5000,
	// 	"DeltaRoundMillis": 2000,
	// 	"DeltaGraceMillis": 500,
	// 	"DeltaCertifiedCommitRequestMillis": 1000,
	// 	"DeltaStageMillis": 30000,
	// 	"MaxRoundsPerEpoch": 10,
	// 	"TransmissionSchedule": [
	// 	  1,
	// 	  1,
	// 	  1,
	// 	  1
	// 	],
	// 	"MaxDurationQueryMillis": 1000,
	// 	"MaxDurationObservationMillis": 1000,
	// 	"MaxDurationReportMillis": 1000,
	// 	"MaxDurationAcceptMillis": 1000,
	// 	"MaxDurationTransmitMillis": 1000,
	// 	"MaxFaultyOracles": 3
	//   }

	signers, transmitters, f, onchainConfig, offchainConfigVersion, offchainConfig, err := ocr3confighelper.ContractSetConfigArgsForTests(
		5000*time.Millisecond,  // DeltaProgress
		5000*time.Millisecond,  // DeltaResend
		5000*time.Millisecond,  // DeltaInitial
		2000*time.Millisecond,  // DeltaRound = MaxDurationQuery + MaxDurationObservation
		500*time.Millisecond,   // DeltaGrace
		1000*time.Millisecond,  // DeltaCertifiedCommitRequest
		30000*time.Millisecond, // DeltaStage
		10,                     // MaxRoundsPerEpoch
		[]int{1},               // TransmissionSchedule
		[]confighelper.OracleIdentityExtra{
			{
				OracleIdentity: confighelper.OracleIdentity{
					OnchainPublicKey:  oracleIdentity.PublicKey,
					OffchainPublicKey: oracleIdentity.OffchainPublicKey,
					PeerID:            oracleIdentity.PeerID,
					TransmitAccount:   types.Account(oracleIdentity.EVMKey),
				},
				ConfigEncryptionPublicKey: oracleIdentity.ConfigEncryptionPublicKey,
			},
		},
		nil,
		// These are timeouts for the execution of plugin methods.
		// Low timeouts will cause random errors when the plugin is too slow but this shouldn't affect tests.
		1000*time.Millisecond, // MaxDurationQuery
		1000*time.Millisecond, // MaxDurationObservation
		1000*time.Millisecond, // MaxDurationShouldAcceptAttestedReport
		1000*time.Millisecond, // MaxDurationShouldTransmitAcceptedReport
		0,                     // F, number of faulty nodes.
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get contract config args: %v", err)
	}

	config := Config{
		ConfigCount:           1,
		Signers:               signers,
		Transmitters:          transmitters,
		F:                     f,
		OnchainConfig:         onchainConfig,
		OffchainConfigVersion: offchainConfigVersion,
		OffchainConfig:        offchainConfig,
	}
	contractConfig, err := config.ContractConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get contract config: %v", err)
	}

	return &contractConfigTracker{
		logger: logger,
		config: contractConfig,
	}, nil
}

func (cct *contractConfigTracker) Notify() <-chan struct{} {
	return nil
}

// TODO: Implement the LatestConfigDetails method
func (cct *contractConfigTracker) LatestConfigDetails(ctx context.Context) (
	changedInBlock uint64,
	configDigest types.ConfigDigest,
	err error,
) {
	cct.logger.Debugf("CCT: Returning latest config details: %s", cct.config.ConfigDigest)

	return 0, cct.config.ConfigDigest, nil
}

// TODO: Implement the LatestConfig method
func (cct *contractConfigTracker) LatestConfig(ctx context.Context, changedInBlock uint64) (types.ContractConfig, error) {
	return cct.config, nil
}

// TODO: Implement the LatestBlockHeight method
func (cct *contractConfigTracker) LatestBlockHeight(ctx context.Context) (blockHeight uint64, err error) {
	return 0, nil
}
