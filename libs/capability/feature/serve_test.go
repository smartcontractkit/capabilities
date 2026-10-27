package feature_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"

	"github.com/smartcontractkit/capabilities/libs/capability/feature"
)

func TestRespond(t *testing.T) {
	m := request(t, devPackage, true)
	metadata := capabilities.ResponseMetadata{CapDON_N: 4}
	attestation := &capabilities.OCRAttestation{ConfigDigest: [32]byte{1}}

	for _, production := range []bool{false, true} {
		response, err := feature.Respond(m, production, metadata, attestation)
		require.NoError(t, err)
		assert.Equal(t, metadata, response.Metadata)
		assert.Same(t, attestation, response.OCRAttestation)
		want, err := feature.MarshalAny(m, production)
		require.NoError(t, err)
		assert.Equal(t, want.GetTypeUrl(), response.Payload.GetTypeUrl())
	}

	_, err := feature.Respond(request(t, prodPackage, false), true, metadata, nil)
	assert.Error(t, err)
}

func TestForwardTriggers(t *testing.T) {
	events := make(chan capabilities.TriggerAndId[*dynamicpb.Message], 1)
	responses := feature.ForwardTriggers(make(chan struct{}), "trigger@1.0.0", true, events)

	events <- capabilities.TriggerAndId[*dynamicpb.Message]{Id: "event-id", Trigger: request(t, devPackage, true)}
	response := receive(t, responses)
	require.NoError(t, response.Err)
	assert.Equal(t, "trigger@1.0.0", response.Event.TriggerType)
	assert.Equal(t, "event-id", response.Event.ID)
	assert.Equal(t, "type.googleapis.com/"+prodPackage+".Request", response.Event.Payload.GetTypeUrl())

	close(events)
	_, open := <-responses
	assert.False(t, open)
}

func TestForwardTriggersError(t *testing.T) {
	events := make(chan capabilities.TriggerAndId[*dynamicpb.Message], 1)
	responses := feature.ForwardTriggers(make(chan struct{}), "trigger@1.0.0", true, events)

	// A message with no production counterpart can't be sent as production.
	events <- capabilities.TriggerAndId[*dynamicpb.Message]{Id: "event-id", Trigger: request(t, prodPackage, false)}
	assert.Error(t, receive(t, responses).Err)
}

func TestForwardTriggersStops(t *testing.T) {
	stop := make(chan struct{})
	events := make(chan capabilities.TriggerAndId[*dynamicpb.Message], 1)
	responses := feature.ForwardTriggers(stop, "trigger@1.0.0", false, events)

	// The event is never received, so only stop can end the forwarding.
	events <- capabilities.TriggerAndId[*dynamicpb.Message]{Id: "event-id", Trigger: request(t, devPackage, true)}
	close(stop)
	require.Eventually(t, func() bool {
		select {
		case _, open := <-responses:
			return !open
		default:
			return false
		}
	}, 10*time.Second, time.Millisecond)
}

func receive(t *testing.T, responses <-chan capabilities.TriggerResponse) capabilities.TriggerResponse {
	t.Helper()

	select {
	case response := <-responses:
		return response
	case <-time.After(10 * time.Second):
		t.Fatal("no response")
		return capabilities.TriggerResponse{}
	}
}
