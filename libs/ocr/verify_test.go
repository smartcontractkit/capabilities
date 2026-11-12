package ocr

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/libocr/offchainreporting2plus/ocr3types"
	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys"
	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"
)

func TestVerifier(t *testing.T) {
	t.Parallel()
	evm, err := ocr2key.New(corekeys.EVM)
	require.NoError(t, err)
	aptos, err := ocr2key.New(corekeys.Aptos)
	require.NoError(t, err)

	publicKey := multichainKey(t, evm, aptos)

	digest := ocrtypes.ConfigDigest{1}
	for family, bundle := range map[string]ocr2key.KeyBundle{"evm": evm, "aptos": aptos} {
		signature, err := bundle.Sign(ocrtypes.ReportContext{ReportTimestamp: ocrtypes.ReportTimestamp{ConfigDigest: digest, Epoch: 5}}, []byte("report"))
		require.NoError(t, err)

		report := ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: family}
		assert.True(t, verify(publicKey, digest, 5, report, signature), family)
		assert.False(t, verify(publicKey, digest, 6, report, signature), family)
	}

	assert.False(t, verify(publicKey, digest, 5, ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: "nope"}, nil))
}

// multichainKey encodes each bundle's public key the way a configuration lists a signer: per family, its type, its
// length, then the key.
func multichainKey(t *testing.T, bundles ...ocr2key.KeyBundle) ocrtypes.OnchainPublicKey {
	t.Helper()
	var encoded []byte
	for _, bundle := range bundles {
		typ, err := bundle.ChainType().Type()
		require.NoError(t, err)
		key := bundle.PublicKey()
		encoded = append(encoded, typ)
		encoded = binary.LittleEndian.AppendUint16(encoded, uint16(len(key))) //nolint:gosec // keys are small
		encoded = append(encoded, key...)
	}

	return encoded
}
