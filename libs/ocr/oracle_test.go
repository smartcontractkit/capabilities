package ocr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// TestOraclesNew checks an oracle is made by the injected maker, with the node's parts filled in over the
// capability's.
func TestOraclesNew(t *testing.T) {
	t.Parallel()
	network, signing, err := embeddedDependencies(newNetwork(), 0, 4)
	require.NoError(t, err)

	var made libocr.OCR3OracleArgs2[echoInfo]
	oracles := Oracles{network: network, signing: signing, newOracle: func(args libocr.OracleArgs) (libocr.Oracle, error) {
		made = args.(libocr.OCR3OracleArgs2[echoInfo])
		return nil, nil
	}}

	_, err = oracles.New(libocr.OCR3OracleArgs2[echoInfo]{
		ReportingPluginFactory: echoPluginFactory{},
		LocalConfig:            ocrtypes.LocalConfig{DatabaseTimeout: time.Second},
	})
	require.NoError(t, err)

	assert.Equal(t, echoPluginFactory{}, made.ReportingPluginFactory, "the capability's own args are kept")
	assert.Equal(t, time.Second, made.LocalConfig.DatabaseTimeout)
	assert.True(t, made.LocalConfig.SkipContractConfigConfirmations, "the local config is adjusted for registry config")
	assert.Equal(t, network.BinaryNetworkEndpointFactory, made.BinaryNetworkEndpointFactory)
	assert.Equal(t, signing.OffchainKeyring, made.OffchainKeyring)
	assert.Equal(t, signing.ContractConfigTracker, made.ContractConfigTracker)
	assert.NotNil(t, made.OnchainKeyring)
	assert.NotNil(t, made.ContractTransmitter)
}
