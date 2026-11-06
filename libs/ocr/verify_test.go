package ocr

import (
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

	publicKey, err := ocr2key.MarshalMultichainKeyBundle(map[string]ocr2key.KeyBundle{"evm": evm, "aptos": aptos})
	require.NoError(t, err)

	digest := ocrtypes.ConfigDigest{1}
	for family, bundle := range map[string]ocr2key.KeyBundle{"evm": evm, "aptos": aptos} {
		signature, err := bundle.Sign(ocr2key.OCR3ReportContext(digest, 5), []byte("report"))
		require.NoError(t, err)

		report := ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: family}
		assert.True(t, verify(publicKey, digest, 5, report, signature), family)
		assert.False(t, verify(publicKey, digest, 6, report, signature), family)
	}

	assert.False(t, verify(publicKey, digest, 5, ocr3types.ReportWithInfo[string]{Report: []byte("report"), Info: "nope"}, nil))
}
