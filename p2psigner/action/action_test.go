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
	"github.com/smartcontractkit/chainlink-protos/cre/go/values"

	"github.com/smartcontractkit/capabilities/libs/testutils"
	"github.com/smartcontractkit/capabilities/p2psigner/action"
	"github.com/smartcontractkit/capabilities/p2psigner/signercap"
)

type mockKeystore struct {
	accounts     []string
	signFunc     func(ctx context.Context, account string, msg []byte) ([]byte, error)
	accountsFunc func(ctx context.Context) ([]string, error)
}

func (m *mockKeystore) Decrypt(ctx context.Context, account string, encrypted []byte) (decrypted []byte, err error) {
	panic("not used by tests")
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
		assert.Equal(t, "p2psigner-action@1.0.0", info.ID)
		assert.Equal(t, capabilities.CapabilityType("action"), info.CapabilityType)
		assert.Equal(t, "Signs a message using the P2P signing key.", info.Description)
		assert.Equal(t, true, info.IsLocal)
	})
}

func TestCapability_Execute(t *testing.T) {
	t.Run("capability executes without error", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accounts: []string{core.P2PAccountKey},
			signFunc: func(ctx context.Context, account string, msg []byte) ([]byte, error) {
				return []byte("test-signature"), nil
			},
		}

		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		digest := []byte("test-digest")
		signInputs := signercap.SignInputs{
			Digest: digest,
		}

		signInputsValue, err := values.WrapMap(signInputs)
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
			"SignInputs": signInputsValue,
		}))
		assert.NoError(t, err)
		assert.NotNil(t, resp.Value)

		// Verify response structure
		var signOutputs signercap.SignOutputs
		err = resp.Value.UnwrapTo(&signOutputs)
		assert.NoError(t, err)
		assert.Equal(t, core.P2PAccountKey, signOutputs.AccountID)
		assert.Equal(t, []byte("test-signature"), signOutputs.Signature)
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
		assert.Equal(t, "missing SignInputs in request", err.Error())
	})

	t.Run("capability errors when SignInputs is missing", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{}}})
		assert.Error(t, err)
		assert.Equal(t, "missing SignInputs in request", err.Error())
	})

	t.Run("capability errors when SignInputs is nil", func(t *testing.T) {
		mockKeystore := &mockKeystore{}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"SignInputs": nil,
			}}})
		assert.Error(t, err)
		assert.Equal(t, "missing SignInputs in request", err.Error())
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

		digest := []byte("test-digest")
		signInputs := signercap.SignInputs{
			Digest: digest,
		}

		signInputsValue, err := values.WrapMap(signInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"SignInputs": signInputsValue,
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

		digest := []byte("test-digest")
		signInputs := signercap.SignInputs{
			Digest: digest,
		}

		signInputsValue, err := values.WrapMap(signInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"SignInputs": signInputsValue,
			}},
		})
		assert.Error(t, err)
		assert.Equal(t, "no accounts found in keystore", err.Error())
	})

	t.Run("capability errors when P2P account not found", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accounts: []string{"OTHER_ACCOUNT"},
		}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		digest := []byte("test-digest")
		signInputs := signercap.SignInputs{
			Digest: digest,
		}

		signInputsValue, err := values.WrapMap(signInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"SignInputs": signInputsValue,
			}},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no P2P_SIGNER account found in keystore")
	})

	t.Run("capability errors when signing fails", func(t *testing.T) {
		mockKeystore := &mockKeystore{
			accounts: []string{core.P2PAccountKey},
			signFunc: func(ctx context.Context, account string, msg []byte) ([]byte, error) {
				return nil, errors.New("signing error")
			},
		}
		c, err := action.New(action.Params{
			Logger:   logger.Test(t),
			Keystore: mockKeystore,
		})
		assert.NoError(t, err)

		digest := []byte("test-digest")
		signInputs := signercap.SignInputs{
			Digest: digest,
		}

		signInputsValue, err := values.WrapMap(signInputs)
		require.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"SignInputs": signInputsValue,
			}},
		})
		assert.Error(t, err)
		assert.Equal(t, "signing error", err.Error())
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
