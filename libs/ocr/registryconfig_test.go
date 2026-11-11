package ocr

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
)

func TestRegistryConfig(t *testing.T) {
	t.Parallel()
	cc := ocrtypes.ContractConfig{
		ConfigDigest: ocrtypes.ConfigDigest{0, 14, 1},
		ConfigCount:  2,
		Signers:      []ocrtypes.OnchainPublicKey{{1}},
		Transmitters: []ocrtypes.Account{"0xf98665742002D7212B9a74c37Bb17dcd0f96FF9D"},
	}

	reg := &fakeRegistry{configs: map[string]ocrtypes.ContractConfig{capabilitiespb.OCR3ConfigDefaultKey: cc}}
	c := &registryConfig{registry: reg, capabilityID: "test@1.0.0", donID: 3}

	count, digest, err := c.LatestConfigDetails(t.Context())
	require.NoError(t, err)
	assert.Equal(t, uint64(2), count)
	assert.Equal(t, cc.ConfigDigest, digest)

	latest, err := c.LatestConfig(t.Context(), count)
	require.NoError(t, err)
	assert.Equal(t, cc, latest)

	digest, err = c.ConfigDigest(t.Context(), latest)
	require.NoError(t, err)
	assert.Equal(t, cc.ConfigDigest, digest)

	assert.Equal(t, "test@1.0.0", reg.capabilityID)
	assert.Equal(t, uint32(3), reg.donID)

	_, err = c.ConfigDigest(t.Context(), ocrtypes.ContractConfig{})
	require.ErrorContains(t, err, "no digest")

	reg.configs = nil
	_, _, err = c.LatestConfigDetails(t.Context())
	require.ErrorContains(t, err, "no OCR config")
}

// fakeRegistry only serves capability configs.
type fakeRegistry struct {
	registry.CapabilitiesRegistry
	configs      map[string]ocrtypes.ContractConfig
	capabilityID string
	donID        uint32
}

func (r *fakeRegistry) ConfigForCapability(_ context.Context, capabilityID string, donID uint32) (capabilities.CapabilityConfiguration, error) {
	r.capabilityID = capabilityID
	r.donID = donID
	return capabilities.CapabilityConfiguration{Ocr3Configs: r.configs}, nil
}
