package ocr

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/keystore/corekeys/ocr2key"

	ocrtypes "github.com/smartcontractkit/libocr/offchainreporting2plus/types"
)

// The encoding is not this package's to choose - it is the one already in the
// configs these oracles join - so the EVM view of it is asserted as bytes rather
// than only round-tripped. A round trip would pass just as well against an
// encoding no config uses. The codec itself is ocr2key's, and tested there.
func TestMarshalEVMOnchainPublicKey(t *testing.T) {
	t.Parallel()

	// A 20 byte EVM address, which is what an EVM bundle's PublicKey is.
	key := ocrtypes.OnchainPublicKey{
		0x1a, 0x0e, 0x37, 0x3a, 0x3b, 0xcb, 0x04, 0x96, 0xb0, 0x23,
		0x0a, 0xef, 0x64, 0x2f, 0x03, 0xcf, 0x52, 0x8b, 0x9f, 0x9c,
	}

	encoded, err := marshalEVMOnchainPublicKey(key)
	require.NoError(t, err)

	// family 1 (EVM), then the length as a little-endian uint16, then the key.
	want := append(ocrtypes.OnchainPublicKey{0x01, 0x14, 0x00}, key...)
	assert.Equal(t, want, encoded)

	got, err := ocr2key.OnchainPublicKeyFor(EVMFamily, encoded)
	require.NoError(t, err)
	assert.Equal(t, key, got)
}

func TestOnchainPublicKeysRejectsNoEVMEntry(t *testing.T) {
	t.Parallel()

	// Aptos only: nothing here can check an EVM signature.
	_, err := ocr2key.OnchainPublicKeyFor(EVMFamily, ocrtypes.OnchainPublicKey{0x05, 0x01, 0x00, 0xaa})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no evm entry")
}
