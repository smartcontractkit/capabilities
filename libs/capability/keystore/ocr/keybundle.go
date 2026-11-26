package ocr

import (
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

// NewKeyBundle is a key bundle of chainType with new keys, made as the node's keystore makes them, so its reports
// carry the chain's own signatures.
func NewKeyBundle(chainType string) (KeyBundle, error) {
	kb, err := ocr2key.New(corekeys.ChainType(chainType))
	if err != nil {
		return nil, err
	}

	return keyBundle{kb}, nil
}

type keyBundle struct {
	ocr2key.KeyBundle
}

func (b keyBundle) ChainType() string { return string(b.KeyBundle.ChainType()) }
