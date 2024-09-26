package oracle

import (
	"context"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ types.ContractConfigTracker = (*contractConfigTracker)(nil)

type contractConfigTracker struct {
	logger logger.Logger
	config types.ContractConfig
}

func NewContractConfigTracker(logger logger.Logger) (*contractConfigTracker, error) {
	config := Config{
		ConfigCount: 1,
		Signers:     nil,
		// Signers:               []types.OnchainPublicKey{},
		Transmitters: nil,
		// Transmitters:          []types.Account{},
		F:             0,
		OnchainConfig: nil,
		// OnchainConfig:         []byte{},
		OffchainConfigVersion: 30,
		OffchainConfig:        nil,
		// OffchainConfig:        []byte{},
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
