package target

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/smartcontractkit/chainlink-common/pkg/beholder"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
	"github.com/smartcontractkit/chainlink-common/pkg/values"

	"github.com/smartcontractkit/capabilities/libs/testutils"
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
		assert.Equal(t, "workflowevent-target@1.0.0", info.ID)
		assert.Equal(t, capabilities.CapabilityType("target"), info.CapabilityType)
		assert.Equal(t, "Emits messages through an OTEL client", info.Description)
		assert.Equal(t, true, info.IsLocal)
	})
}

func TestCapability_Execute(t *testing.T) {
	t.Run("capability executes without error", func(t *testing.T) {
		emitter := &mockEmitter{EmitFn: func(ctx context.Context, body []byte, attrKVs ...any) error {
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

		_, err = c.Execute(context.Background(), workflow.NewRequest(map[string]any{
			"payload": payload,
		}))
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

	t.Run("capability execute errors when the client creation errors", func(t *testing.T) {
		oldNewClientFn := newClientFn
		newClientFn = func(cfg beholder.Config) (*beholder.Client, error) {
			return nil, errors.New("client boom")
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

		_, err = c.Execute(context.Background(), workflow.NewRequest(map[string]any{
			"payload": payload,
		}))
		assert.Error(t, err)
		assert.Equal(t, "client boom", err.Error())
	})

	t.Run("capability runs with a custom otel endpoint", func(t *testing.T) {
		emitter := &mockEmitter{EmitFn: func(ctx context.Context, body []byte, attrKVs ...any) error {
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
			assert.Equal(t, "redpanda:1234", cfg.OtelExporterGRPCEndpoint)
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

		config, err := values.NewMap(map[string]interface{}{
			"otelEndpoint": "redpanda:1234",
		})
		assert.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"payload": payload,
			}},
			Config: config,
		})
		assert.NoError(t, err)
	})

	t.Run("capability errors when an unsupported type is passed in the config", func(t *testing.T) {
		emitter := &mockEmitter{EmitFn: func(ctx context.Context, body []byte, attrKVs ...any) error {
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

		config, err := values.NewMap(map[string]interface{}{
			"otelEndpoint": big.NewInt(123),
		})
		assert.NoError(t, err)

		_, err = c.Execute(context.Background(), capabilities.CapabilityRequest{
			Inputs: &values.Map{Underlying: map[string]values.Value{
				"payload": payload,
			}},
			Config: config,
		})
		assert.Error(t, err)
		assert.Equal(t, `decoding failed due to the following error(s):

'otelEndpoint' expected type 'string', got unconvertible type 'values.BigInt', value: '&{123}'`, err.Error())
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
