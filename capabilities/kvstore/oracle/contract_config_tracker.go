package oracle

import (
	"context"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

var _ types.ContractConfigTracker = (*contractConfigTracker)(nil)

type contractConfigTracker struct{}

func NewContractConfigTracker() *contractConfigTracker {
	return &contractConfigTracker{}
}

func (cct *contractConfigTracker) Notify() <-chan struct{} {
	return nil
}

// TODO: Implement the LatestConfigDetails method
func (cct *contractConfigTracker) LatestConfigDetails(ctx context.Context) (changedInBlock uint64, configDigest types.ConfigDigest, err error) {
	return 0, types.ConfigDigest{}, nil
}

// TODO: Implement the LatestConfig method
func (cct *contractConfigTracker) LatestConfig(ctx context.Context, changedInBlock uint64) (types.ContractConfig, error) {
	return types.ContractConfig{}, nil
}

// TODO: Implement the LatestBlockHeight method
func (cct *contractConfigTracker) LatestBlockHeight(ctx context.Context) (blockHeight uint64, err error) {
	return 0, nil
}
