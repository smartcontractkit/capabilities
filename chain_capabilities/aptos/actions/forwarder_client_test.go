package actions

import (
	"testing"

	aptos_sdk "github.com/aptos-labs/aptos-go-sdk"
	"github.com/stretchr/testify/require"
)

func TestReceiverBytesToAddress(t *testing.T) {
	t.Run("accepts 32-byte receiver", func(t *testing.T) {
		expected := aptos_sdk.AccountAddress{}
		copy(expected[:], []byte{
			0x01, 0x02, 0x03, 0x04,
			0x05, 0x06, 0x07, 0x08,
			0x09, 0x0a, 0x0b, 0x0c,
			0x0d, 0x0e, 0x0f, 0x10,
			0x11, 0x12, 0x13, 0x14,
			0x15, 0x16, 0x17, 0x18,
			0x19, 0x1a, 0x1b, 0x1c,
			0x1d, 0x1e, 0x1f, 0x20,
		})

		addr, err := receiverBytesToAddress(expected[:])
		require.NoError(t, err)
		require.Equal(t, expected, addr)
	})

	t.Run("rejects non-32-byte receiver", func(t *testing.T) {
		_, err := receiverBytesToAddress([]byte{0x01, 0x02, 0x03})
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid receiver length")
	})
}
