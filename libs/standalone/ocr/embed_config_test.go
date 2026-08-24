package ocr

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3confighelper"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/standalone"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

const testCapabilityID = "consensus@1.0.0-alpha"

func TestEmbeddedOCRConfig(t *testing.T) {
	t.Run("is one configuration every instance computes identically", func(t *testing.T) {
		first, err := EmbeddedOCRConfig(testCapabilityID, 1, "", 4)
		require.NoError(t, err)
		second, err := EmbeddedOCRConfig(testCapabilityID, 1, "", 4)
		require.NoError(t, err)

		// The digest is the whole point: oracles that compute different ones never speak, and they
		// each compute their own.
		assert.Equal(t, first.ConfigDigest, second.ConfigDigest)
		assert.Equal(t, first.Signers, second.Signers)
		assert.Equal(t, first.Transmitters, second.Transmitters)
		assert.Equal(t, first.OffchainConfig, second.OffchainConfig)
	})

	t.Run("names a different configuration per capability, DON and OCR instance", func(t *testing.T) {
		base, err := EmbeddedOCRConfig(testCapabilityID, 1, "", 4)
		require.NoError(t, err)

		otherCapability, err := EmbeddedOCRConfig("other@1.0.0", 1, "", 4)
		require.NoError(t, err)
		otherDON, err := EmbeddedOCRConfig(testCapabilityID, 2, "", 4)
		require.NoError(t, err)
		otherKey, err := EmbeddedOCRConfig(testCapabilityID, 1, "second", 4)
		require.NoError(t, err)

		assert.NotEqual(t, base.ConfigDigest, otherCapability.ConfigDigest)
		assert.NotEqual(t, base.ConfigDigest, otherDON.ConfigDigest)
		assert.NotEqual(t, base.ConfigDigest, otherKey.ConfigDigest)
	})

	t.Run("is a configuration libocr accepts", func(t *testing.T) {
		// One and four because those are the ends of it: one instance is a DON that tolerates no
		// fault (F=0), and four is the smallest that tolerates one.
		for _, oracles := range []int{1, 2, 3, 4, 7} {
			config, err := EmbeddedOCRConfig(testCapabilityID, 1, "", oracles)
			require.NoError(t, err)

			// The same decoding an oracle does on the configuration it is handed: the identity lists
			// have to agree in length, hold no duplicates, and the protocol parameters have to pass
			// libocr's own bounds - all of which this asserts by not erroring.
			public, err := ocr3confighelper.PublicConfigFromContractConfig(false, config)
			require.NoError(t, err, "%d oracles", oracles)

			assert.Len(t, public.OracleIdentities, oracles)
			assert.Equal(t, (oracles-1)/3, public.F)
		}
	})

	t.Run("refuses a DON of no oracles", func(t *testing.T) {
		_, err := EmbeddedOCRConfig(testCapabilityID, 1, "", 0)
		require.ErrorContains(t, err, "at least one is required")
	})
}

// TestEmbeddedOCRConfigListsEveryInstance is the check that matters most: libocr recognises an
// oracle by matching all four parts of its identity against the configuration, and an instance that
// matches three of them is not a member at all. So this asserts, for what each instance resolves for
// itself, exactly what SharedConfigFromContractConfig asserts.
func TestEmbeddedOCRConfigListsEveryInstance(t *testing.T) {
	const oracles = 4

	config, err := EmbeddedOCRConfig(testCapabilityID, 1, "", oracles)
	require.NoError(t, err)
	public, err := ocr3confighelper.PublicConfigFromContractConfig(false, config)
	require.NoError(t, err)

	for i := range oracles {
		// The OCR form, since it is an oracle's identity that a configuration lists.
		dep := &embedded{lggr: logger.Test(t), index: i, instances: oracles}
		factories, err := dep.Get(t.Context(), standalone.CommonConfig{})
		require.NoError(t, err)

		identity := public.OracleIdentities[i]
		assert.Equal(t, factories.PeerID.String(), identity.PeerID, "instance %d", i)
		assert.Equal(t, factories.TransmitAccount, identity.TransmitAccount, "instance %d", i)
		assert.Equal(t, factories.Offchain.OffchainPublicKey(), identity.OffchainPublicKey, "instance %d", i)
		// What the keyring answers with is what libocr compares to the signer entry, byte for byte.
		assert.True(t, bytes.Equal(factories.Onchain.PublicKey(), identity.OnchainPublicKey), "instance %d", i)
	}
}

// An embedded instance has nothing to dial: its peers are goroutines beside it.
func TestEmbeddedFactoriesHaveNoBootstrappers(t *testing.T) {
	dep := &embedded{lggr: logger.Test(t), index: 1, instances: 3}

	factories, err := dep.Get(t.Context(), standalone.CommonConfig{})
	require.NoError(t, err)

	assert.Empty(t, factories.Bootstrappers)
	assert.NotEmpty(t, factories.TransmitAccount, "an embedded oracle reports the account its configuration lists")
}

// An instance outside the run it was told about would be a member no configuration lists, and says
// so rather than spending the run unrecognised.
func TestEmbeddedOCRRefusesAnInstanceOutsideTheRun(t *testing.T) {
	dep := &embedded{lggr: logger.Test(t), index: 3, instances: 1}

	_, err := dep.Get(t.Context(), standalone.CommonConfig{})
	require.ErrorContains(t, err, "instance 3 is not one of the 1 instances")
}

func TestEmbeddedOCRConfigRegistry(t *testing.T) {
	registry := embeddedOCRConfigRegistry(4)

	got, err := registry.OCRConfig(t.Context(), testCapabilityID, 1, "")
	require.NoError(t, err)

	want, err := EmbeddedOCRConfig(testCapabilityID, 1, "", 4)
	require.NoError(t, err)
	assert.Equal(t, want, got)
}
