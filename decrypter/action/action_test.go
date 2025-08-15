package action_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/types/core"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/decrypter/action"
	"github.com/smartcontractkit/capabilities/decrypter/decryptercap"
	"github.com/smartcontractkit/capabilities/libs/testutils"
)

type mockKeystore struct {
	accounts     []string
	signFunc     func(ctx context.Context, account string, msg []byte) ([]byte, error)
	decryptFunc  func(ctx context.Context, account string, ctxt []byte) ([]byte, error)
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

func (m *mockKeystore) Decrypt(ctx context.Context, account string, ctxt []byte) ([]byte, error) {
	if m.decryptFunc != nil {
		return m.decryptFunc(ctx, account, ctxt)
	}
	return []byte("mock-decrypted-message"), nil
}

func TestNew(t *testing.T) {
	t.Run("a new p2psigner action is created", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)
		assert.NotNil(t, c)
	})
}

func TestCapability_Info(t *testing.T) {
	t.Run("capability info is reported correctly", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)
		info, err := c.Info(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "decrypter-action@1.0.0", info.ID)
		assert.Equal(t, capabilities.CapabilityType("action"), info.CapabilityType)
		assert.Equal(t, "Decrypts a message using the workflow key.", info.Description)
		assert.Equal(t, true, info.IsLocal)
	})
}

func TestCapability_Execute(t *testing.T) {
	t.Run("capability executes without error", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accounts: []string{core.StandardCapabilityAccount},
			decryptFunc: func(ctx context.Context, account string, msg []byte) ([]byte, error) {
				return []byte("test-plaintext"), nil
			},
		}

		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		ctxt := []byte("test-ciphertext")
		decryptInputs := decryptercap.DecryptInputs{
			Ciphertexts: [][]byte{ctxt},
		}

		decryptInputsValue, err := values.WrapMap(decryptInputs)
		require.NoError(t, err)

		ctx := context.Background()
		workflow, removeWorkflow := testutils.NewWorkflow(ctx, testutils.WorkflowParams{
			T: t,
			Capabilities: []testutils.CapabilityWithConfig{
				{
					Capability: c,
				},
			},
			Owner: "owner1",
		})
		defer removeWorkflow(ctx)

		resp, err := c.Execute(context.Background(), workflow.NewRequest(map[string]any{
			"DecryptInputs": decryptInputsValue,
		}))
		assert.NoError(t, err)
		assert.NotNil(t, resp.Value)

		// Verify response structure
		var decryptOutputs decryptercap.DecryptOutputs
		err = resp.Value.UnwrapTo(&decryptOutputs)
		assert.NoError(t, err)
		assert.Equal(t, core.StandardCapabilityAccount, decryptOutputs.AccountID)
		assert.Equal(t, []byte("test-plaintext"), decryptOutputs.Plaintext)
	})

	t.Run("capability errors when inputs is nil", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: nil,
		})
		assert.Error(t, err)
		assert.Equal(t, "missing DecryptInputs in request", err.Error())
	})

	t.Run("capability errors when DecryptInputs is missing", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{}}})
		assert.Error(t, err)
		assert.Equal(t, "missing DecryptInputs in request", err.Error())
	})

	t.Run("capability errors when DecryptInputs is nil", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"DecryptInputs": nil,
			}}})
		assert.Error(t, err)
		assert.Equal(t, "missing DecryptInputs in request", err.Error())
	})

	t.Run("capability errors when keystore accounts fails", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accountsFunc: func(ctx context.Context) ([]string, error) {
				return nil, errors.New("keystore error")
			},
		}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		ctxt := []byte("test-ciphertext")
		decryptInputs := decryptercap.DecryptInputs{
			Ciphertexts: [][]byte{ctxt},
		}

		decryptInputsValue, err := values.WrapMap(decryptInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"DecryptInputs": decryptInputsValue,
			}},
		})
		assert.Error(t, err)
		assert.Equal(t, "keystore error", err.Error())
	})

	t.Run("capability errors when no accounts found", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accounts: []string{},
		}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		ctxt := []byte("test-ciphertext")
		decryptInputs := decryptercap.DecryptInputs{
			Ciphertexts: [][]byte{ctxt},
		}

		decryptInputsValue, err := values.WrapMap(decryptInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"DecryptInputs": decryptInputsValue,
			}},
		})
		assert.Error(t, err)
		assert.Equal(t, "no accounts found in keystore", err.Error())
	})

	t.Run("capability errors when StandardCapabilities account not found", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accounts: []string{"OTHER_ACCOUNT"},
		}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		ctxt := []byte("test-ciphertext")
		decryptInputs := decryptercap.DecryptInputs{
			Ciphertexts: [][]byte{ctxt},
		}

		decryptInputsValue, err := values.WrapMap(decryptInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"DecryptInputs": decryptInputsValue,
			}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no STANDARD_CAPABILITY_ACCOUNT account found in keystore")
	})

	t.Run("capability errors when signing fails", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accounts: []string{core.StandardCapabilityAccount},
			decryptFunc: func(ctx context.Context, account string, ctxt []byte) ([]byte, error) {
				return nil, errors.New("decrypting error")
			},
		}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		ctxt := []byte("test-ciphertext")
		decryptInputs := decryptercap.DecryptInputs{
			Ciphertexts: [][]byte{ctxt},
		}

		decryptInputsValue, err := values.WrapMap(decryptInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"DecryptInputs": decryptInputsValue,
			}},
		})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "failed to decrypt any ciphertexts: failed to decrypt ciphertext")
	})
}

func TestCapability_RegisterToWorkflow(t *testing.T) {
	t.Run("register to workflow does not error", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)
		err = c.RegisterToWorkflow(context.Background(), capabilities.RegisterToWorkflowRequest{})
		assert.NoError(t, err)
	})
}

func TestCapability_UnregisterFromWorkflow(t *testing.T) {
	t.Run("unregister from workflow does not error", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)
		err = c.UnregisterFromWorkflow(context.Background(), capabilities.UnregisterFromWorkflowRequest{})
		assert.NoError(t, err)
	})
}
