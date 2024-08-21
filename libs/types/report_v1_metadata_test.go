package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReportV1Metadata(t *testing.T) {

	t.Run("fails with invalid data", func(t *testing.T) {
		data := []byte{0, 1, 2, 3}

		_, err := DecodeReportV1Metadata(data)
		assert.ErrorContains(t, err, "data too short:")
	})

	t.Run("succeeds with valid data", func(t *testing.T) {
		metadata := ReportV1Metadata{
			Timestamp:        1234567890,
			DonID:            1,
			DonConfigVersion: 1,
			ReportID:         [2]byte{0, 1},
		}
		data, err := metadata.Encode()
		require.NoError(t, err)

		decodedMetadata, err := DecodeReportV1Metadata(data)
		assert.NoError(t, err)
		assert.Equal(t, metadata, decodedMetadata)
	})
}
