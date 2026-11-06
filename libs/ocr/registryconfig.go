package ocr

import (
	"context"
	"fmt"

	gethcommon "github.com/ethereum/go-ethereum/common"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
	"github.com/smartcontractkit/chainlink-common/pkg/ocrutil"
)

var (
	_ ocrtypes.ContractConfigTracker  = (*registryConfig)(nil)
	_ ocrtypes.OffchainConfigDigester = (*registryConfig)(nil)
)

// registryConfig is the config of a capability's oracle as the capabilities registry holds it, with the digest the
// node's own oracles compute for it.
type registryConfig struct {
	registry        registry.CapabilitiesRegistry
	capabilityID    string
	donID           uint32
	chainID         uint64
	registryAddress string
}

func (c *registryConfig) Notify() <-chan struct{} { return nil }

func (c *registryConfig) LatestConfigDetails(ctx context.Context) (uint64, ocrtypes.ConfigDigest, error) {
	cc, err := c.latest(ctx)
	if err != nil {
		return 0, ocrtypes.ConfigDigest{}, err
	}

	return cc.ConfigCount, cc.ConfigDigest, nil
}

func (c *registryConfig) LatestConfig(ctx context.Context, _ uint64) (ocrtypes.ContractConfig, error) {
	return c.latest(ctx)
}

// LatestBlockHeight is the config count, as the registry has no blocks; ocrutil.ForRegistryConfig skips the
// confirmations it would count.
func (c *registryConfig) LatestBlockHeight(ctx context.Context) (uint64, error) {
	cc, err := c.latest(ctx)
	if err != nil {
		return 0, err
	}

	return cc.ConfigCount, nil
}

func (c *registryConfig) ConfigDigest(_ context.Context, cc ocrtypes.ContractConfig) (ocrtypes.ConfigDigest, error) {
	withDigest, err := c.withDigest(cc)
	if err != nil {
		return ocrtypes.ConfigDigest{}, err
	}

	return withDigest.ConfigDigest, nil
}

func (c *registryConfig) ConfigDigestPrefix(context.Context) (ocrtypes.ConfigDigestPrefix, error) {
	return ocrtypes.ConfigDigestPrefixKeystoneOCR3Capability, nil
}

func (c *registryConfig) latest(ctx context.Context) (ocrtypes.ContractConfig, error) {
	cfg, err := c.registry.ConfigForCapability(ctx, c.capabilityID, c.donID)
	if err != nil {
		return ocrtypes.ContractConfig{}, err
	}

	cc, ok := cfg.Ocr3Configs[capabilitiespb.OCR3ConfigDefaultKey]
	if !ok {
		return ocrtypes.ContractConfig{}, fmt.Errorf("the registry has no OCR config for %s on DON %d", c.capabilityID, c.donID)
	}

	return c.withDigest(cc)
}

// withDigest is cc with its digest, and its transmitters as the checksummed addresses the node's own oracles see, so
// that libocr finds the transmit account among them.
func (c *registryConfig) withDigest(cc ocrtypes.ContractConfig) (ocrtypes.ContractConfig, error) {
	cc, err := ocrutil.WithRegistryConfigDigest(c.chainID, c.registryAddress, c.capabilityID, c.donID,
		capabilitiespb.OCR3ConfigDefaultKey, cc)
	if err != nil {
		return ocrtypes.ContractConfig{}, err
	}

	transmitters := make([]ocrtypes.Account, len(cc.Transmitters))
	for i, t := range cc.Transmitters {
		if !gethcommon.IsHexAddress(string(t)) {
			return ocrtypes.ContractConfig{}, fmt.Errorf("transmitter %q is not an address", t)
		}

		transmitters[i] = ocrtypes.Account(gethcommon.HexToAddress(string(t)).Hex())
	}

	cc.Transmitters = transmitters
	return cc, nil
}
