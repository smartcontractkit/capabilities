package service

import (
	"testing"
	"time"

	"github.com/samber/do/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/capabilities/libs/capability"
	"github.com/smartcontractkit/capabilities/libs/capability/capabilitytest"
	"github.com/smartcontractkit/capabilities/libs/capability/internal/example/capability/protos"
	"github.com/smartcontractkit/capabilities/libs/capability/internal/example/capability/protos/protostest"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/logger"
)

var metadata = capabilities.RequestMetadata{WorkflowID: "workflow-id"}

// instances is how many instances the tests run: a node runs its capability alone, a DON runs several together.
const instances = 4

func start(t *testing.T, cfg Config, n int) protostest.ClientInstances {
	t.Helper()

	return capabilitytest.Start(t, New, n, capability.WithProvider(func(i *capability.Injector) error {
		return i.ProvideValue(&cfg)
	}))
}

func TestNew(t *testing.T) {
	c := start(t, Config{}, 1)[0]

	info, err := c.Info(t.Context())
	require.NoError(t, err)
	assert.Equal(t, protos.ClientID, info.ID)
	assert.NotEmpty(t, info.Description)
	assert.NoError(t, c.Ready())
}

func TestLifecycle(t *testing.T) {
	s := newService(logger.Test(t), Config{})
	require.Error(t, s.Ready())
	require.Error(t, s.HealthReport()[s.Name()])

	require.NoError(t, s.Start(t.Context()))
	require.NoError(t, s.Ready())
	require.NoError(t, s.HealthReport()[s.Name()])

	require.NoError(t, s.Close())
	require.Error(t, s.Ready())
	require.Error(t, s.HealthReport()[s.Name()])
}

func TestNewWithoutDependencies(t *testing.T) {
	_, err := New(do.New())
	assert.Error(t, err)
}

func TestTrigger(t *testing.T) {
	caps := start(t, Config{}, instances)
	input := &protos.TriggerConfig{Message: "hello"}

	events, err := caps.RegisterTrigger(t.Context(), "trigger-id", metadata, input)
	require.NoError(t, err)

	select {
	case e := <-events:
		require.Nil(t, e.Mismatch)
		require.NoError(t, e.Err)
		assert.Equal(t, input.Message, e.Trigger.Message)
	case <-time.After(10 * time.Second):
		t.Fatal("no event")
	}

	require.NoError(t, caps.UnregisterTrigger(t.Context(), "trigger-id", metadata, input))
}

func TestExecute(t *testing.T) {
	reply, err := start(t, Config{}, instances).Execute(t.Context(), metadata, &protos.Request{Message: "hello"})
	require.NoError(t, err)
	assert.Equal(t, "hello", reply.Response.Message)
}
