package oracle

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

var _ types.ContractConfigTracker = (*contractConfigTracker)(nil)

type Config struct {
	ConfigCount           uint64
	Signers               []types.OnchainPublicKey
	Transmitters          []types.Account
	F                     uint8
	OnchainConfig         []byte
	OffchainConfigVersion uint64
	OffchainConfig        []byte
}

func NewConfigFromContractConfig(cc types.ContractConfig) *Config {
	return &Config{
		ConfigCount:           cc.ConfigCount,
		Signers:               cc.Signers,
		Transmitters:          cc.Transmitters,
		F:                     cc.F,
		OnchainConfig:         cc.OnchainConfig,
		OffchainConfigVersion: cc.OffchainConfigVersion,
		OffchainConfig:        cc.OffchainConfig,
	}
}

func (c *Config) Digest() (types.ConfigDigest, error) {
	// Serialize the new struct to bytes
	configBytes, err := json.Marshal(c)
	if err != nil {
		return types.ConfigDigest{}, fmt.Errorf("failed to marshal oracle config: %v", err)
	}

	configDigestPrefix := make([]byte, 2)
	binary.BigEndian.PutUint16(configDigestPrefix, uint16(types.ConfigDigestPrefixEVMSimple))

	configHash := sha256.Sum256(configBytes)
	configDigestBytes := append(configDigestPrefix[:], configHash[2:]...)
	return types.BytesToConfigDigest(configDigestBytes[:])
}

func (c *Config) ContractConfig() (types.ContractConfig, error) {
	configDigest, err := c.Digest()
	if err != nil {
		return types.ContractConfig{}, fmt.Errorf("failed getting a config digest: %v", err)
	}

	return types.ContractConfig{
		ConfigDigest:          configDigest,
		ConfigCount:           c.ConfigCount,
		Signers:               c.Signers,
		Transmitters:          c.Transmitters,
		F:                     c.F,
		OnchainConfig:         c.OnchainConfig,
		OffchainConfigVersion: c.OffchainConfigVersion,
		OffchainConfig:        c.OffchainConfig,
	}, nil
}
