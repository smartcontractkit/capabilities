package oracle

import (
	"github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

var _ types.OffchainConfigDigester = (*offchainConfigDigester)(nil)

type offchainConfigDigester struct{}

func NewOffchainConfigDigester() *offchainConfigDigester {
	return &offchainConfigDigester{}
}

func (ocd *offchainConfigDigester) ConfigDigest(types.ContractConfig) (types.ConfigDigest, error) {
	return types.ConfigDigest{}, nil
}

func (ocd *offchainConfigDigester) ConfigDigestPrefix() (types.ConfigDigestPrefix, error) {
	return types.ConfigDigestPrefixEVMSimple, nil
}
