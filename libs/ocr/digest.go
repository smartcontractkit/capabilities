package ocr

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	capabilitiespb "github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
)

// ConfigDigest computes the digest identifying an OCR3 configuration held in a
// CapabilitiesRegistry contract.
//
// The contract stores the configuration without a digest, so whoever reads it
// computes this. What goes into it is what makes a configuration this one: the
// chain and address it was read from, so a configuration cannot be replayed
// against another registry; the DON, capability and OCR instance it belongs to,
// so two instances of one capability cannot be confused for each other; and the
// configuration itself.
//
// Every process running or serving one of these configurations has to agree on
// the digest, byte for byte, or their oracles will not talk to each other. That
// is why this is here rather than copied into each of them.
//
// The OCR3 in the name of the configuration is not a version this file is
// limited to by accident: the digest carries
// ConfigDigestPrefixKeystoneOCR3Capability in its first two bytes, which libocr
// treats as a domain separator, so a digest made here is only ever valid for a
// keystone capability's protocol instance. Everything else on the way past -
// the database, the keyrings, ContractConfig itself - is the shared
// offchainreporting2plus vocabulary and carries no version.
func ConfigDigest(
	chainID uint64,
	registryAddress string,
	capabilityID string,
	donID uint32,
	ocrConfigKey string,
	cfg *capabilitiespb.OCR3Config,
) (ocrtypes.ConfigDigest, error) {
	buf := []byte{}

	// 1. Chain ID (8 bytes, big-endian)
	var chainIDBytes [8]byte
	binary.BigEndian.PutUint64(chainIDBytes[:], chainID)
	buf = append(buf, chainIDBytes[:]...)

	// 2. Registry address (length-prefixed)
	buf = appendLengthPrefixed(buf, []byte(registryAddress))

	// 3. DON ID (4 bytes, big-endian)
	var donIDBytes [4]byte
	binary.BigEndian.PutUint32(donIDBytes[:], donID)
	buf = append(buf, donIDBytes[:]...)

	// 4. Capability ID (length-prefixed)
	buf = appendLengthPrefixed(buf, []byte(capabilityID))

	// 5. OCR config key (length-prefixed)
	buf = appendLengthPrefixed(buf, []byte(ocrConfigKey))

	// 6. Config count (8 bytes, big-endian)
	var configCountBytes [8]byte
	binary.BigEndian.PutUint64(configCountBytes[:], cfg.ConfigCount)
	buf = append(buf, configCountBytes[:]...)

	// 7. Number of signers (1 byte)
	if len(cfg.Signers) > math.MaxUint8 {
		return ocrtypes.ConfigDigest{}, fmt.Errorf("too many signers: %d > %d", len(cfg.Signers), math.MaxUint8)
	}
	buf = append(buf, uint8(len(cfg.Signers))) //#nosec G115

	// 8. Each signer (length-prefixed)
	for _, signer := range cfg.Signers {
		buf = appendLengthPrefixed(buf, signer)
	}

	// 9. Each transmitter (length-prefixed)
	for _, transmitter := range cfg.Transmitters {
		buf = appendLengthPrefixed(buf, transmitter)
	}

	// 10. F (1 byte)
	if cfg.F > math.MaxUint8 {
		return ocrtypes.ConfigDigest{}, fmt.Errorf("f value too large: %d > %d", cfg.F, math.MaxUint8)
	}
	buf = append(buf, uint8(cfg.F)) //#nosec G115

	// 11. Onchain config (length-prefixed)
	buf = appendLengthPrefixed(buf, cfg.OnchainConfig)

	// 12. Offchain config version (8 bytes, big-endian)
	var offchainVersionBytes [8]byte
	binary.BigEndian.PutUint64(offchainVersionBytes[:], cfg.OffchainConfigVersion)
	buf = append(buf, offchainVersionBytes[:]...)

	// 13. Offchain config (length-prefixed)
	buf = appendLengthPrefixed(buf, cfg.OffchainConfig)

	// Hash and create digest with prefix in first 2 bytes
	hash := sha256.Sum256(buf)
	var digest ocrtypes.ConfigDigest
	binary.BigEndian.PutUint16(digest[:2], uint16(ocrtypes.ConfigDigestPrefixKeystoneOCR3Capability))
	copy(digest[2:], hash[2:])

	return digest, nil
}

func appendLengthPrefixed(buf []byte, data []byte) []byte {
	var lenBytes [4]byte
	binary.BigEndian.PutUint32(lenBytes[:], uint32(len(data))) //#nosec G115 - data length will never exceed uint32 max in practice
	buf = append(buf, lenBytes[:]...)
	buf = append(buf, data...)
	return buf
}
