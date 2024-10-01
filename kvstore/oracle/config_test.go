package oracle_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/kvstore/oracle"
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

func TestReportV1Metadata(t *testing.T) {
	t.Run("succeeds with valid data", func(t *testing.T) {
		config := oracle.Config{
			ConfigCount:           1,
			Signers:               []types.OnchainPublicKey{},
			Transmitters:          []types.Account{},
			F:                     0,
			OnchainConfig:         []byte{},
			OffchainConfigVersion: 1,
			OffchainConfig:        []byte{},
		}

		contractConfig, err := config.ContractConfig()
		assert.NoError(t, err)
		assert.Equal(t, contractConfig.ConfigDigest.String(), "000134abd0d063f3229387df9d935899e082356ae9adc119e0e7b5bdf0c5e6ff")
	})
}
