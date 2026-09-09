package ui

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/pb"
)

// The trigger the tests subscribe to is Executable.Execute, which is the
// streaming method of the same real service the rest of these tests use. So its
// event is a CapabilityResponse, whose Error field is a convenient thing to make
// two instances disagree about.
func eventType() reflect.Type { return reflect.TypeFor[*pb.CapabilityResponse]() }

func event(t *testing.T, id, payload string) capabilities.TriggerResponse {
	t.Helper()
	wrapped, err := anypb.New(&pb.CapabilityResponse{Error: payload})
	require.NoError(t, err)
	return capabilities.TriggerResponse{Event: capabilities.TriggerEvent{ID: id, Payload: wrapped}}
}

// fakeTrigger is one instance's trigger capability, whose events the test
// delivers by hand.
//
// A channel per registration rather than one for the capability: the point of
// most of these tests is several instances delivering the same event, which is
// several registrations.
type fakeTrigger struct {
	registerErr error

	mu           sync.Mutex
	channels     []chan capabilities.TriggerResponse
	registered   []capabilities.TriggerRegistrationRequest
	unregistered []capabilities.TriggerRegistrationRequest
	acked        []string
}

func (f *fakeTrigger) Info(context.Context) (capabilities.CapabilityInfo, error) {
	return capabilities.NewCapabilityInfo(testCapabilityID, capabilities.CapabilityTypeTrigger, "a trigger for tests")
}

func (f *fakeTrigger) RegisterTrigger(_ context.Context, request capabilities.TriggerRegistrationRequest) (<-chan capabilities.TriggerResponse, error) {
	if f.registerErr != nil {
		return nil, f.registerErr
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan capabilities.TriggerResponse, 16)
	f.channels = append(f.channels, ch)
	f.registered = append(f.registered, request)
	return ch, nil
}

func (f *fakeTrigger) UnregisterTrigger(_ context.Context, request capabilities.TriggerRegistrationRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.unregistered = append(f.unregistered, request)
	return nil
}

func (f *fakeTrigger) AckEvent(_ context.Context, triggerID, eventID, method string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acked = append(f.acked, fmt.Sprintf("%s/%s/%s", triggerID, eventID, method))
	return nil
}

// deliver sends an event down the nth registration this capability handed out.
func (f *fakeTrigger) deliver(t *testing.T, n int, response capabilities.TriggerResponse) {
	t.Helper()
	f.mu.Lock()
	require.Greater(t, len(f.channels), n, "registration %d was never made", n)
	ch := f.channels[n]
	f.mu.Unlock()
	ch <- response
}

func (f *fakeTrigger) requests() []capabilities.TriggerRegistrationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capabilities.TriggerRegistrationRequest(nil), f.registered...)
}

func (f *fakeTrigger) unregisters() []capabilities.TriggerRegistrationRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]capabilities.TriggerRegistrationRequest(nil), f.unregistered...)
}

func (f *fakeTrigger) acks() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.acked...)
}

// join subscribes one instance to a trigger ID.
func join(t *testing.T, h *Hub, triggerID string, instance int, trigger capabilities.TriggerExecutable) (*Status, error) {
	t.Helper()
	payload, err := anypb.New(&pb.CapabilityRequest{})
	require.NoError(t, err)

	return h.subscribe(registration{
		triggerID:    triggerID,
		capabilityID: testCapabilityID,
		service:      string(testService().FullName()),
		method:       "Execute",
		instance:     instance,
		label:        fmt.Sprintf("instance %d", instance+1),
		trigger:      trigger,
		payload:      payload,
		eventType:    eventType(),
	})
}

// eventually waits for a condition the hub reaches on a goroutine of its own.
func eventually(t *testing.T, why string, condition func() bool) {
	t.Helper()
	require.Eventually(t, condition, 2*time.Second, time.Millisecond, why)
}

// A subscription registers the trigger with the ID and method it was asked for.
func TestSubscribeRegistersTheTrigger(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	status, err := join(t, h, "ui-trigger-abc", 0, trigger)
	require.NoError(t, err)

	assert.Equal(t, "ui-trigger-abc", status.TriggerID)
	assert.Equal(t, "Execute", status.Method)
	assert.Equal(t, testCapabilityID, status.CapabilityID)
	require.Len(t, status.Instances, 1)
	assert.Equal(t, "instance 1", status.Instances[0].Label)

	requests := trigger.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, "ui-trigger-abc", requests[0].TriggerID)
	assert.Equal(t, "Execute", requests[0].Method)
	assert.NotNil(t, requests[0].Payload, "the form's input is what the trigger is configured with")
}

// The trigger ID is the subscription, so a second instance registering the same
// one joins it rather than starting another. This is what makes "two instances
// now, a third later" one table.
func TestSubscribingTheSameTriggerIDJoins(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	first, second := &fakeTrigger{}, &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-shared", 0, first)
	require.NoError(t, err)

	status, err := join(t, h, "ui-trigger-shared", 3, second)
	require.NoError(t, err)

	require.Len(t, status.Instances, 2)
	assert.Equal(t, 0, status.Instances[0].Instance)
	assert.Equal(t, 3, status.Instances[1].Instance)
	assert.Len(t, h.List(), 1, "one subscription, not one per instance")
}

// A trigger ID already watching something else would put two unrelated streams in
// one table, so it is refused - and refused as the caller's mistake, since a
// different ID fixes it.
func TestSubscribingTheSameTriggerIDToAnotherMethodIsRefused(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	_, err := join(t, h, "ui-trigger-taken", 0, &fakeTrigger{})
	require.NoError(t, err)

	payload, err := anypb.New(&pb.CapabilityRequest{})
	require.NoError(t, err)
	_, err = h.subscribe(registration{
		triggerID:    "ui-trigger-taken",
		capabilityID: testCapabilityID,
		method:       "SomethingElse",
		instance:     1,
		trigger:      &fakeTrigger{},
		payload:      payload,
		eventType:    eventType(),
	})
	require.Error(t, err)
	assert.True(t, isUserError(err), "%v", err)
	assert.Contains(t, err.Error(), "already subscribed")
}

// Registering one instance twice under one trigger ID would register it twice in
// the capability, so it is refused rather than done.
func TestSubscribingOneInstanceTwiceIsRefused(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-dup", 2, trigger)
	require.NoError(t, err)

	_, err = join(t, h, "ui-trigger-dup", 2, trigger)
	require.Error(t, err)
	assert.True(t, isUserError(err), "%v", err)
	assert.Len(t, trigger.requests(), 1, "the second attempt must not reach the capability")
}

// A registration the capability refuses is not left behind as a subscription
// nothing is attached to.
func TestFailedRegistrationLeavesNoSubscription(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	_, err := join(t, h, "ui-trigger-bad", 0, &fakeTrigger{registerErr: fmt.Errorf("no such schedule")})
	require.Error(t, err)
	assert.Empty(t, h.List())
}

// Two instances delivering the same event is one row with a column each, and one
// payload hash: they agree.
func TestAgreeingInstancesAreOneRow(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	first, second := &fakeTrigger{}, &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-agree", 0, first)
	require.NoError(t, err)
	_, err = join(t, h, "ui-trigger-agree", 1, second)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-agree")
	require.NoError(t, err)

	first.deliver(t, 0, event(t, "event-1", "same"))
	second.deliver(t, 0, event(t, "event-1", "same"))

	eventually(t, "both instances should appear in the row", func() bool {
		rows := s.rows()
		return len(rows) == 1 && len(rows[0].Nodes) == 2
	})

	rows := s.rows()
	require.Len(t, rows, 1)
	assert.Equal(t, "event-1", rows[0].ID)
	assert.Len(t, rows[0].PayloadIDs, 1, "identical payloads hash the same")
	assert.False(t, rows[0].Diverged)

	// One payload, held once on the row rather than repeated per instance, and both
	// nodes point at it.
	require.Len(t, rows[0].Payloads, 1)
	assert.JSONEq(t, `{"error":"same"}`, string(rows[0].Payloads[0]))
	assert.Equal(t, 0, rows[0].Nodes[0].PayloadIndex)
	assert.Equal(t, 0, rows[0].Nodes[1].PayloadIndex)
}

// The payloads and their hashes are in the same order, and a node says which of
// them it sent - which is what lets the table put a payload per column and have
// the instance row point into it.
func TestPayloadsAndHashesLineUp(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	first, second, third := &fakeTrigger{}, &fakeTrigger{}, &fakeTrigger{}
	for i, trigger := range []*fakeTrigger{first, second, third} {
		_, err := join(t, h, "ui-trigger-lineup", i, trigger)
		require.NoError(t, err)
	}

	s, err := h.get("ui-trigger-lineup")
	require.NoError(t, err)

	// Instances 1 and 3 agree, instance 2 does not. Delivered in instance order so
	// the arrival order is the one the row is expected to keep.
	first.deliver(t, 0, event(t, "event-1", "agreed"))
	eventually(t, "the first instance should be recorded", func() bool {
		return len(s.rows()) == 1 && len(s.rows()[0].Nodes) == 1
	})
	second.deliver(t, 0, event(t, "event-1", "different"))
	eventually(t, "the second instance should be recorded", func() bool {
		return len(s.rows()[0].Nodes) == 2
	})
	third.deliver(t, 0, event(t, "event-1", "agreed"))
	eventually(t, "the third instance should be recorded", func() bool {
		return len(s.rows()[0].Nodes) == 3
	})

	row := s.rows()[0]
	assert.True(t, row.Diverged)

	// Two distinct payloads, in the order they arrived, and as many hashes as
	// payloads.
	require.Len(t, row.Payloads, 2)
	require.Len(t, row.PayloadIDs, 2)
	assert.JSONEq(t, `{"error":"agreed"}`, string(row.Payloads[0]))
	assert.JSONEq(t, `{"error":"different"}`, string(row.Payloads[1]))

	// And each instance points at the one it sent.
	assert.Equal(t, 0, row.Nodes[0].PayloadIndex)
	assert.Equal(t, 1, row.Nodes[1].PayloadIndex)
	assert.Equal(t, 0, row.Nodes[2].PayloadIndex, "the third instance agreed with the first")

	// The hash at an index is the hash of the payload at that index.
	for i, node := range row.Nodes {
		assert.Equal(t, row.PayloadIDs[node.PayloadIndex], node.PayloadID, "node %d", i)
	}
}

// An instance that failed has no payload to point at, so it points at nothing
// rather than at payload zero - which would read as agreeing with it.
func TestAFailedDeliveryPointsAtNoPayload(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-failed", 0, trigger)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-failed")
	require.NoError(t, err)

	trigger.deliver(t, 0, capabilities.TriggerResponse{
		Event: capabilities.TriggerEvent{ID: "event-1"},
		Err:   fmt.Errorf("the schedule was withdrawn"),
	})
	eventually(t, "the failure should be recorded", func() bool { return len(s.rows()) == 1 })

	row := s.rows()[0]
	require.Len(t, row.Nodes, 1)
	assert.Equal(t, -1, row.Nodes[0].PayloadIndex)
	assert.Contains(t, row.Nodes[0].Error, "the schedule was withdrawn")
	assert.Empty(t, row.Payloads)
	assert.False(t, row.Diverged, "one instance failing is not two instances disagreeing")
}

// Instances disagreeing is the bug the table exists to show, so the row says so
// rather than showing one of the two.
func TestDisagreeingInstancesDiverge(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	first, second := &fakeTrigger{}, &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-differ", 0, first)
	require.NoError(t, err)
	_, err = join(t, h, "ui-trigger-differ", 1, second)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-differ")
	require.NoError(t, err)

	first.deliver(t, 0, event(t, "event-1", "this"))
	second.deliver(t, 0, event(t, "event-1", "that"))

	eventually(t, "both instances should appear in the row", func() bool {
		rows := s.rows()
		return len(rows) == 1 && len(rows[0].Nodes) == 2
	})

	rows := s.rows()
	assert.True(t, rows[0].Diverged)
	assert.Len(t, rows[0].PayloadIDs, 2)
	assert.NotEqual(t, rows[0].Nodes[0].PayloadID, rows[0].Nodes[1].PayloadID)
}

// The kept-event count is bounded, so a trigger firing all day does not grow
// without limit.
func TestEventsAreTrimmedToTheRing(t *testing.T) {
	h := NewHub()
	h.ring = 3
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-ring", 0, trigger)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-ring")
	require.NoError(t, err)

	for i := range 6 {
		trigger.deliver(t, 0, event(t, fmt.Sprintf("event-%d", i), "payload"))
	}

	eventually(t, "the oldest events should be dropped", func() bool {
		rows := s.rows()
		return len(rows) == 3 && rows[0].ID == "event-3"
	})
}

// Nobody watching means nobody wants it, once the grace period has passed: this
// is what closing the window does.
func TestAbandonedSubscriptionsAreUnregistered(t *testing.T) {
	h := NewHub()
	h.grace = 10 * time.Millisecond
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-abandoned", 0, trigger)
	require.NoError(t, err)

	eventually(t, "the trigger should be unregistered", func() bool {
		return len(trigger.unregisters()) == 1
	})
	assert.Empty(t, h.List())
}

// A reader arriving inside the grace period is somebody coming back, which is
// what a reload looks like - so the subscription is still there.
func TestAReaderCancelsTheGracePeriod(t *testing.T) {
	h := NewHub()
	h.grace = 50 * time.Millisecond
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-kept", 0, trigger)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-kept")
	require.NoError(t, err)

	c := newClient()
	s.addClient(c)

	time.Sleep(150 * time.Millisecond)
	assert.Empty(t, trigger.unregisters(), "a watched subscription must not be unregistered")
	assert.Len(t, h.List(), 1)

	// And leaving starts the clock again.
	s.removeClient(c)
	eventually(t, "the trigger should be unregistered once the reader leaves", func() bool {
		return len(trigger.unregisters()) == 1
	})
}

// A reader that reattaches is sent the table it left, which is what makes a
// reload look like nothing happened.
func TestASnapshotCarriesTheTable(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-snapshot", 0, trigger)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-snapshot")
	require.NoError(t, err)

	trigger.deliver(t, 0, event(t, "event-1", "first"))
	trigger.deliver(t, 0, event(t, "event-2", "second"))
	eventually(t, "both events should be recorded", func() bool { return len(s.rows()) == 2 })

	snapshot := s.addClient(newClient())
	require.Len(t, snapshot.Rows, 2)
	assert.Equal(t, "event-1", snapshot.Rows[0].ID)
	assert.Equal(t, 1, snapshot.Readers)
}

// A reader watching is sent each event as it arrives.
func TestReadersAreSentRows(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	trigger := &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-live", 0, trigger)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-live")
	require.NoError(t, err)

	c := newClient()
	s.addClient(c)

	trigger.deliver(t, 0, event(t, "event-1", "hello"))

	select {
	case m := <-c.messages:
		assert.Equal(t, MessageRow, m.Type)
		require.NotNil(t, m.Row)
		assert.Equal(t, "event-1", m.Row.ID)
	case <-time.After(2 * time.Second):
		t.Fatal("the reader was sent nothing")
	}
}

// Acknowledging goes to the instances that delivered the event, and carries the
// method, because that is what a capability keys its delivery on.
func TestAckReachesTheInstancesThatDelivered(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	first, second := &fakeTrigger{}, &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-ack", 0, first)
	require.NoError(t, err)
	_, err = join(t, h, "ui-trigger-ack", 1, second)
	require.NoError(t, err)

	s, err := h.get("ui-trigger-ack")
	require.NoError(t, err)

	// Only the first instance delivers it.
	first.deliver(t, 0, event(t, "event-1", "hello"))
	eventually(t, "the event should be recorded", func() bool { return len(s.rows()) == 1 })

	require.NoError(t, h.Ack("ui-trigger-ack", "event-1"))
	assert.Equal(t, []string{"ui-trigger-ack/event-1/Execute"}, first.acks())
	assert.Empty(t, second.acks(), "an instance that never delivered it has nothing to acknowledge")
}

// Acking something that was never delivered is the caller's mistake, not a broken
// page.
func TestAckOfAnUnknownEventIsAUserError(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	_, err := join(t, h, "ui-trigger-noack", 0, &fakeTrigger{})
	require.NoError(t, err)

	err = h.Ack("ui-trigger-noack", "never-happened")
	require.Error(t, err)
	assert.True(t, isUserError(err), "%v", err)
}

// Closing a subscription unregisters every instance and takes it out of the list.
func TestUnsubscribeClosesEverything(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	first, second := &fakeTrigger{}, &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-close", 0, first)
	require.NoError(t, err)
	_, err = join(t, h, "ui-trigger-close", 1, second)
	require.NoError(t, err)

	require.NoError(t, h.Unsubscribe("ui-trigger-close", nil))
	assert.Len(t, first.unregisters(), 1)
	assert.Len(t, second.unregisters(), 1)
	assert.Empty(t, h.List())
}

// Detaching one instance leaves the rest of the subscription running, which is how
// "stop instance 3 and watch the others" works.
func TestUnsubscribeCanDetachOneInstance(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	first, second := &fakeTrigger{}, &fakeTrigger{}
	_, err := join(t, h, "ui-trigger-partial", 0, first)
	require.NoError(t, err)
	_, err = join(t, h, "ui-trigger-partial", 1, second)
	require.NoError(t, err)

	require.NoError(t, h.Unsubscribe("ui-trigger-partial", []int{1}))
	assert.Empty(t, first.unregisters())
	assert.Len(t, second.unregisters(), 1)

	require.Len(t, h.List(), 1)
	assert.Len(t, h.List()[0].Instances, 1)
}

// A subscription that was never opened is not something the page can watch.
func TestStreamingAnUnknownTriggerIDIsAUserError(t *testing.T) {
	h := NewHub()
	t.Cleanup(func() { _ = h.Close() })

	_, err := h.get("ui-trigger-nothing")
	require.Error(t, err)
	assert.True(t, isUserError(err), "%v", err)
}
