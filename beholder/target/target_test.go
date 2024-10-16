package target

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"
)

func TestNew(t *testing.T) {
	t.Run("a new beholder capability is created", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		assert.NotNil(t, c)
	})
}

func TestCapability_Info(t *testing.T) {
	t.Run("capability info is reported correctly", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		info, err := c.Info(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "beholder-target@1.0.0", info.ID)
		assert.Equal(t, capabilities.CapabilityType("target"), info.CapabilityType)
		assert.Equal(t, "Emits messages through beholder", info.Description)
		assert.Equal(t, true, info.IsLocal)
	})
}

func TestCapability_Execute(t *testing.T) {
	t.Run("capability executes without error", func(t *testing.T) {
		emitter := &mockEmitter{EmitFn: func(ctx context.Context, body []byte, attrKVs ...any) error {
			wantAttributes := []any{
				"beholder_data_schema",
				"/custom-message/versions/1",
				"beholder_data_type",
				"custom_message",
				"workflow_id",
				"my workflow",
				"execution_id",
				"12345",
				"workflow_name",
				"event capability test",
				"workflow_owner",
				"cool dude",
			}

			assert.Equal(t, wantAttributes, attrKVs)

			var valueMap values.Map
			pbm := values.ProtoMap(&valueMap)
			err := unmarshalFn(body, pbm)
			assert.NoError(t, err)

			rawMap := map[string]any{}
			for k, v := range pbm.Fields {
				rawMap[k] = v.GetStringValue()
			}

			assert.Equal(t, rawMap, map[string]any{"service": "Beholder", "component": "Unit test"})
			return nil
		}}

		mockBeholderClient := &beholder.Client{
			Emitter: emitter,
		}

		oldNewClientFn := newClientFn
		newClientFn = func(cfg beholder.Config) (*beholder.Client, error) {
			return mockBeholderClient, nil
		}
		defer func() {
			newClientFn = oldNewClientFn
		}()

		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)

		payload, err := values.NewMap(map[string]any{
			"service":   values.NewString("Beholder"),
			"component": values.NewString("Unit test"),
		})
		assert.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"payload": payload,
			}},
			Metadata: capabilities.RequestMetadata{
				WorkflowID:          "my workflow",
				WorkflowOwner:       "cool dude",
				WorkflowExecutionID: "12345",
				WorkflowName:        "event capability test",
			},
		})
		assert.NoError(t, err)
	})

	t.Run("capability errors when inputs is nil", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: nil,
		})
		assert.Error(t, err)
		assert.Equal(t, "missing inputs field", err.Error())
	})

	t.Run("capability errors when payload is missing", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{}}})
		assert.Error(t, err)
		assert.Equal(t, "missing payload", err.Error())
	})

	t.Run("capability errors when payload is nil", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"payload": nil,
			}}})
		assert.Error(t, err)
		assert.Equal(t, "missing payload", err.Error())
	})

	t.Run("capability errors when payload is not a map", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"payload": values.NewString("test"),
			}}})
		assert.Error(t, err)
		assert.Equal(t, "payload is not a map", err.Error())
	})

	t.Run("capability errors when marshalling errors", func(t *testing.T) {
		oldMarshal := marshalFn
		marshalFn = func(v proto.Message) ([]byte, error) {
			return nil, errors.New("boom")
		}
		defer func() {
			marshalFn = oldMarshal
		}()

		payload, err := values.NewMap(map[string]any{
			"name": values.NewString("test"),
		})

		assert.NoError(t, err)
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"payload": payload,
			}},
		})
		assert.Error(t, err)
		assert.Equal(t, "boom", err.Error())
	})

	t.Run("capability errors when creating the beholder client errors", func(t *testing.T) {
		oldNewClientFn := newClientFn
		newClientFn = func(cfg beholder.Config) (*beholder.Client, error) {
			return nil, errors.New("new client boom")
		}
		defer func() {
			newClientFn = oldNewClientFn
		}()

		_, err := New(Params{Logger: logger.Test(t)})
		assert.Error(t, err)
		assert.Equal(t, "new client boom", err.Error())
	})

	t.Run("capability errors when emit errors", func(t *testing.T) {
		emitter := &mockEmitter{EmitFn: func(ctx context.Context, body []byte, attrKVs ...any) error {
			return errors.New("emit boom")
		}}

		mockBeholderClient := &beholder.Client{
			Emitter: emitter,
		}

		oldNewClientFn := newClientFn
		newClientFn = func(cfg beholder.Config) (*beholder.Client, error) {
			return mockBeholderClient, nil
		}
		defer func() {
			newClientFn = oldNewClientFn
		}()

		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)

		payload, err := values.NewMap(map[string]any{
			"name": values.NewString("test"),
		})
		assert.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"payload": payload,
			}},
		})
		assert.Error(t, err)
		assert.Equal(t, "emit boom", err.Error())
	})
}

type mockEmitter struct {
	EmitFn func(ctx context.Context, body []byte, attrKVs ...any) error
}

func (e *mockEmitter) Emit(ctx context.Context, body []byte, attrKVs ...any) error {
	return e.EmitFn(ctx, body, attrKVs...)
}

func TestCapability_UnregisterFromWorkflow(t *testing.T) {
	t.Run("unregister from workflow does not error", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		err = c.UnregisterFromWorkflow(context.Background(), capabilities.UnregisterFromWorkflowRequest{})
		assert.NoError(t, err)
	})
}

func TestCapability_RegisterToWorkflow(t *testing.T) {
	t.Run("register to workflow does not error", func(t *testing.T) {
		c, err := New(Params{Logger: logger.Test(t)})
		assert.NoError(t, err)
		err = c.RegisterToWorkflow(context.Background(), capabilities.RegisterToWorkflowRequest{})
		assert.NoError(t, err)
	})
}
