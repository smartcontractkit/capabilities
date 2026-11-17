package ocr

import (
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

func TestMemoryDB(t *testing.T) {
	db := NewMemoryDB("test", logger.Test(t))
	digest := ocrtypes.ConfigDigest{1}

	_, err := db.ReadState(t.Context(), digest)
	require.ErrorContains(t, err, "state not found for oracle test")

	require.NoError(t, db.WriteState(t.Context(), digest, ocrtypes.PersistentState{Epoch: 3}))
	state, err := db.ReadState(t.Context(), digest)
	require.NoError(t, err)
	assert.Equal(t, uint32(3), state.Epoch)

	config, err := db.ReadConfig(t.Context())
	require.NoError(t, err)
	assert.Nil(t, config)

	require.NoError(t, db.WriteConfig(t.Context(), ocrtypes.ContractConfig{ConfigCount: 2}))
	config, err = db.ReadConfig(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(2), config.ConfigCount)

	value, err := db.ReadProtocolState(t.Context(), digest, "key")
	require.NoError(t, err)
	assert.Nil(t, value)

	require.NoError(t, db.WriteProtocolState(t.Context(), digest, "key", []byte("value")))
	value, err = db.ReadProtocolState(t.Context(), digest, "key")
	require.NoError(t, err)
	assert.Equal(t, []byte("value"), value)

	require.NoError(t, db.WriteProtocolState(t.Context(), digest, "key", nil))
	value, err = db.ReadProtocolState(t.Context(), digest, "key")
	require.NoError(t, err)
	assert.Nil(t, value)
}

func TestMemoryDB_PendingTransmissions(t *testing.T) {
	db := NewMemoryDB("test", logger.Test(t))
	digest := ocrtypes.ConfigDigest{1}
	old := ocrtypes.ReportTimestamp{ConfigDigest: digest, Epoch: 1}
	recent := ocrtypes.ReportTimestamp{ConfigDigest: digest, Epoch: 2}
	other := ocrtypes.ReportTimestamp{ConfigDigest: ocrtypes.ConfigDigest{2}, Epoch: 1}
	now := time.Now()

	require.NoError(t, db.StorePendingTransmission(t.Context(), old, ocrtypes.PendingTransmission{Time: now.Add(-time.Hour)}))
	require.NoError(t, db.StorePendingTransmission(t.Context(), recent, ocrtypes.PendingTransmission{Time: now}))
	require.NoError(t, db.StorePendingTransmission(t.Context(), other, ocrtypes.PendingTransmission{Time: now}))

	pending, err := db.PendingTransmissionsWithConfigDigest(t.Context(), digest)
	require.NoError(t, err)
	assert.Len(t, pending, 2)

	require.NoError(t, db.DeletePendingTransmissionsOlderThan(t.Context(), now.Add(-time.Minute)))
	pending, err = db.PendingTransmissionsWithConfigDigest(t.Context(), digest)
	require.NoError(t, err)
	assert.Equal(t, []ocrtypes.ReportTimestamp{recent}, slices.Collect(maps.Keys(pending)))

	require.NoError(t, db.DeletePendingTransmission(t.Context(), recent))
	pending, err = db.PendingTransmissionsWithConfigDigest(t.Context(), digest)
	require.NoError(t, err)
	assert.Empty(t, pending)
}
