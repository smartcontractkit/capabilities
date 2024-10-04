package oracle

import (
	"context"
	"testing"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/testutils"
)

func TestContractConfigTracker_LatestConfigDetails(t *testing.T) {
	log := testutils.NewLogger(t)
	tracker, err := NewContractConfigTracker(log, Identity{})
	require.NoError(t, err)

	changedInBlock, configDigest, err := tracker.LatestConfigDetails(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(11414), changedInBlock)
	assert.NotEqual(t, types.ConfigDigest{}, configDigest)
}

func TestContractConfigTracker_LatestConfig(t *testing.T) {
	log := testutils.NewLogger(t)
	tracker, err := NewContractConfigTracker(log, Identity{})
	require.NoError(t, err)

	config, err := tracker.LatestConfig(context.Background(), 11414)
	require.NoError(t, err)
	assert.Equal(t, "0001a5642679056a5904e8c9d640f9eb3a366517cbd4bf33f4053d43f44fc943", config.ConfigDigest.Hex())
	assert.Equal(t, uint64(21), config.ConfigCount)
}

func TestContractConfigTracker_LatestBlockHeight(t *testing.T) {
	log := testutils.NewLogger(t)
	tracker, err := NewContractConfigTracker(log, Identity{})
	require.NoError(t, err)

	blockHeight, err := tracker.LatestBlockHeight(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uint64(11414), blockHeight)
}
