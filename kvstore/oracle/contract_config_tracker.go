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

// sharedConfig := ocr3config.SharedConfig{
// 	ocr3config.PublicConfig{
// 		deltaProgress,
// 		deltaResend,
// 		deltaInitial,
// 		deltaRound,
// 		deltaGrace,
// 		deltaCertifiedCommitRequest,
// 		deltaStage,
// 		rMax,
// 		s,
// 		identities,
// 		reportingPluginConfig,
// 		maxDurationQuery,
// 		maxDurationObservation,
// 		maxDurationShouldAcceptAttestedReport,
// 		maxDurationShouldTransmitAcceptedReport,
// 		f,
// 		onchainConfig,
// 		types.ConfigDigest{},
// 	},
// 	&sharedSecret,
// }

type contractConfigTracker struct {
	logger logger.Logger
	config types.ContractConfig
}

func NewContractConfigTracker(logger logger.Logger) (*contractConfigTracker, error) {
	var RMax uint64 = 20 // maximum number of rounds that a node can be a leader
	S := []int{}
	var F uint8 = 0 // number of faulty nodes
	N := 1          // number of nodes
	for i := 0; i < N; i++ {
		S = append(S, 1)
	}

	signers, transmitters, f_, onchainConfig_, offchainConfigVersion, offchainConfig, err := ocr3confighelper.ContractSetConfigArgsForTests(
		20*time.Millisecond, // DeltaProgress
		10*time.Millisecond, // DeltaResend
		20*time.Millisecond, // DeltaInitial
		2*time.Nanosecond,   // DeltaRound = MaxDurationQuery + MaxDurationObservation
		0*time.Second,       // DeltaGrace
		10*time.Millisecond, // DeltaCertifiedCommitRequest
		0*time.Millisecond,  // DeltaStage
		RMax,
		S,
		[]confighelper.OracleIdentityExtra{}, // these will be filled out later
		nil,
		// These are timeouts for the execution of plugin methods.
		// Low timeouts will cause random errors when the plugin is too slow but this shouldn't affect tests.
		100*time.Nanosecond, // MaxDurationQuery
		100*time.Nanosecond, // MaxDurationObservation
		100*time.Nanosecond, // MaxDurationShouldAcceptAttestedReport
		100*time.Nanosecond, // MaxDurationShouldTransmitAcceptedReport
		int(F),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get contract config args: %v", err)
	}

	config := Config{
		ConfigCount:           1,
		Signers:               signers,
		Transmitters:          transmitters,
		F:                     f_,
		OnchainConfig:         onchainConfig_,
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
