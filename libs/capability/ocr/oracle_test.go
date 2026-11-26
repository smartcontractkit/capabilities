package ocr

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	libocr "github.com/smartcontractkit/libocr/offchainreporting2plus"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// TestWire checks an oracle's args get the node's parts filled in over the capability's.
func TestWire(t *testing.T) {
	t.Parallel()
	network, err := embeddedNetwork(newNetwork(), 0)
	require.NoError(t, err)
	id := testIdentity(t, 0)

	made := wire(libocr.OCR3OracleArgs2[echoInfo]{
		ReportingPluginFactory: echoPluginFactory{},
		LocalConfig:            ocrtypes.LocalConfig{DatabaseTimeout: time.Second},
	}, network, id)

	assert.Equal(t, echoPluginFactory{}, made.ReportingPluginFactory, "the capability's own args are kept")
	assert.Equal(t, time.Second, made.LocalConfig.DatabaseTimeout)
	assert.True(t, made.LocalConfig.SkipContractConfigConfirmations, "the local config is adjusted for registry config")
	assert.Equal(t, 5*time.Second, made.LocalConfig.ContractConfigTrackerPollInterval)
	assert.Equal(t, network.BinaryNetworkEndpointFactory, made.BinaryNetworkEndpointFactory)
	assert.Equal(t, id.OffchainKeyring, made.OffchainKeyring)
	assert.Equal(t, id.ContractConfigTracker, made.ContractConfigTracker)
	assert.NotNil(t, made.OnchainKeyring)

	account, err := made.ContractTransmitter.FromAccount(t.Context())
	require.NoError(t, err)
	assert.Equal(t, id.TransmitAccount, account, "the transmitter transmits as the node's account")
}

// TestNewRegistryOracleNeedsTheConfig checks a capability whose OCR config the registry doesn't have fails to make
// its oracle, rather than waiting for one.
func TestNewRegistryOracleNeedsTheConfig(t *testing.T) {
	t.Parallel()
	network, err := embeddedNetwork(newNetwork(), 0)
	require.NoError(t, err)

	_, err = NewRegistryOracle(t.Context(), libocr.OCR3OracleArgs2[echoInfo]{}, network, testEmbeddedClient(t, 0),
		&fakeRegistry{}, "test@1.0.0", 0)
	require.Error(t, err)
}
