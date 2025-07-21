package action_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/chainlink-common/pkg/logger"

	action "github.com/smartcontractkit/capabilities/confidential_http_action/action"
	cap "github.com/smartcontractkit/capabilities/confidential_http_action/confidential_http_action_cap"
)

type mockKeystore struct {
	accounts     []string
	signFunc     func(ctx context.Context, account string, msg []byte) ([]byte, error)
	accountsFunc func(ctx context.Context) ([]string, error)
}

func (m *mockKeystore) Accounts(ctx context.Context) ([]string, error) {
	if m.accountsFunc != nil {
		return m.accountsFunc(ctx)
	}
	return m.accounts, nil
}

func (m *mockKeystore) Sign(ctx context.Context, account string, msg []byte) ([]byte, error) {
	if m.signFunc != nil {
		return m.signFunc(ctx, account, msg)
	}
	return []byte("mock-signature"), nil
}

func getTestConfig() cap.Config {
	enclaveTypeA := "TypeA"

	return cap.Config{
		Enclaves: []cap.Enclave{
			{
				EnclaveType:   &enclaveTypeA,
				ExtraData:     []uint8{0x01, 0x02, 0x03},
				ID:            []uint8{0xAA, 0xBB},
				TrustedValues: []uint8{0xDE, 0xAD, 0xBE, 0xEF},
				URL:           "http://enclave-a.example.com",
			},
			{
				EnclaveType:   nil, // Omitting EnclaveType for this one
				ExtraData:     []uint8{0x04, 0x05},
				ID:            []uint8{0xCC, 0xDD},
				TrustedValues: []uint8{0x11, 0x22, 0x33},
				URL:           "https://enclave-b.example.com/api",
			},
		},
		VaultDONID: []uint8{0xF0, 0x0B, 0xAA, 0x42},
	}
}

func TestNew(t *testing.T) {
	t.Run("a new p2psigner action is created", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(logger.Test(t), getTestConfig(), mockKeystore)

		assert.NoError(t, err)
		assert.NotNil(t, c)
	})
}
