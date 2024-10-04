package oracle

import (
	"encoding/hex"
	"fmt"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var _ types.OffchainConfigDigester = (*offchainConfigDigester)(nil)

type offchainConfigDigester struct {
	logger logger.Logger
}

func NewOffchainConfigDigester(logger logger.Logger) *offchainConfigDigester {
	return &offchainConfigDigester{
		logger: logger,
	}
}

func (ocd *offchainConfigDigester) ConfigDigest(cc types.ContractConfig) (types.ConfigDigest, error) {
	ocd.logger.Debug("Calculating config digest for contract config")
	ocd.logger.Debugf("Contract config: %+v", cc)

	// THIS IS A HACK
	configDigestBytes, err := hex.DecodeString("000192171524191d04b8e50ce8b2019cc4297c595dfa1e2420fb161193e49e2e")
	if err != nil {
		return types.ConfigDigest{}, fmt.Errorf("failed to decode config digest: %v", err)
	}

	return types.BytesToConfigDigest(configDigestBytes)
}

func (ocd *offchainConfigDigester) ConfigDigestPrefix() (types.ConfigDigestPrefix, error) {
	return types.ConfigDigestPrefixEVMSimple, nil
}
