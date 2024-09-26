package oracle

import (
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

	config := NewConfigFromContractConfig(cc)
	return config.Digest()
}

func (ocd *offchainConfigDigester) ConfigDigestPrefix() (types.ConfigDigestPrefix, error) {
	return types.ConfigDigestPrefixEVMSimple, nil
}
