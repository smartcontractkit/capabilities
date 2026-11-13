package ocr

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/registry"
)

var (
	_ ocrtypes.ContractConfigTracker  = (*registryConfig)(nil)
	_ ocrtypes.OffchainConfigDigester = (*registryConfig)(nil)
)

// registryConfig is the config of a capability's oracle as the node gives it through the capabilities registry: as
// the node's own oracles run it, digest included.
type registryConfig struct {
	registry     registry.CapabilitiesRegistry
	capabilityID string
	donID        uint32
	// transmitAccount is the node's, as its oracles give it.
	transmitAccount ocrtypes.Account
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

// ConfigDigest is the digest the node gave cc, which is computed from where the registry is, so only the node can.
func (c *registryConfig) ConfigDigest(_ context.Context, cc ocrtypes.ContractConfig) (ocrtypes.ConfigDigest, error) {
	if cc.ConfigDigest == (ocrtypes.ConfigDigest{}) {
		return ocrtypes.ConfigDigest{}, errors.New("the node gave the OCR config no digest")
	}

	return cc.ConfigDigest, nil
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

	// libocr finds this oracle in the config by comparing its transmit account as a string, but the registry gives
	// accounts as bare hex, while the node may give its own with a prefix or checksum.
	transmitters := make([]ocrtypes.Account, len(cc.Transmitters))
	for i, t := range cc.Transmitters {
		transmitters[i] = t
		if bareHex(t) == bareHex(c.transmitAccount) {
			transmitters[i] = c.transmitAccount
		}
	}

	cc.Transmitters = transmitters
	return cc, nil
}

func bareHex(a ocrtypes.Account) string {
	return strings.ToLower(strings.TrimPrefix(string(a), "0x"))
}
