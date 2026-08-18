package registry

import (
	"context"
	"fmt"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/capabilities/libs/ocr"

	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
)

var _ core.OCRConfigRegistry = (*Registry)(nil)

// OCRConfig returns the OCR3 configuration a capability runs under, digest
// included.
//
// A capability hosted in another process is given this rather than the contract
// it came from: the digest is the only part of the contract's identity its
// oracle needs, and computing one here means every consumer of a configuration
// agrees on it by construction rather than by configuring the same chain and
// address twice.
func (r *Registry) OCRConfig(ctx context.Context, capabilityID string, donID uint32, key string) (ocrtypes.ContractConfig, error) {
	cfg, digest, err := r.ocrConfig(ctx, capabilityID, donID, key)
	if err != nil {
		return ocrtypes.ContractConfig{}, err
	}
	return capabilitiespb.OCR3ConfigFromProto(cfg, digest)
}

// ocrConfig resolves one OCR instance's configuration and its digest, in the
// form the contract stores it. The gRPC service passes both on as they are, so
// that a caller decoding the configuration and this computing its digest are
// looking at the same bytes.
func (r *Registry) ocrConfig(ctx context.Context, capabilityID string, donID uint32, key string) (*capabilitiespb.OCR3Config, ocrtypes.ConfigDigest, error) {
	// A capability normally runs one OCR instance, and naming it is a detail
	// only a capability running several has to care about.
	if key == "" {
		key = capabilitiespb.OCR3ConfigDefaultKey
	}

	lr, err := r.snapshot()
	if err != nil {
		return nil, ocrtypes.ConfigDigest{}, err
	}

	// Without a contract there is no digest to compute, and a digest computed
	// from a zero chain and address would be one no other oracle agrees with -
	// which fails as a silently unreachable DON rather than as an error.
	if lr.Contract.Address == "" {
		return nil, ocrtypes.ConfigDigest{}, fmt.Errorf(
			"cannot compute the OCR config digest for capability %s: this registry does not report which contract it reads", capabilityID)
	}

	raw, err := lr.RawConfigForCapability(ctx, capabilityID, donID)
	if err != nil {
		return nil, ocrtypes.ConfigDigest{}, err
	}

	capConfig := &capabilitiespb.CapabilityConfig{}
	if err = proto.Unmarshal(raw, capConfig); err != nil {
		return nil, ocrtypes.ConfigDigest{}, fmt.Errorf(
			"capability %s on DON %d has an unparseable config: %w", capabilityID, donID, err)
	}

	cfg, ok := capConfig.GetOcr3Configs()[key]
	if !ok {
		return nil, ocrtypes.ConfigDigest{}, fmt.Errorf(
			"capability %s on DON %d has no OCR config %q", capabilityID, donID, key)
	}

	digest, err := ocr.ConfigDigest(lr.Contract.ChainID, lr.Contract.Address, capabilityID, donID, key, cfg)
	if err != nil {
		return nil, ocrtypes.ConfigDigest{}, fmt.Errorf(
			"failed to compute the OCR config digest for capability %s on DON %d: %w", capabilityID, donID, err)
	}

	return cfg, digest, nil
}
