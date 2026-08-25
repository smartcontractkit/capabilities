package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jhump/protoreflect/dynamic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// subscriptionKey is how a request names the synthetic method that registers the
// test service's trigger.
func subscriptionKey(method string) string {
	return string(testService().FullName()) + SubscriptionsSuffix + "/" + method
}

// newSubscribableServer is a mounted instance whose registry can resolve a
// trigger, which is what a subscription needs.
func newSubscribableServer(t *testing.T, fleet *Fleet, hub *Hub, index int) (*Server, *fakeTrigger) {
	t.Helper()

	empty, err := anypb.New(&emptypb.Empty{})
	require.NoError(t, err)
	capability := &fakeCapability{response: empty}
	trigger := &fakeTrigger{}

	server, err := New(t.Context(), fakeRegistry{capability: capability, trigger: trigger}, capability)
	require.NoError(t, err)
	require.NoError(t, mount(http.NewServeMux(), server, fleet, hub, index, fmt.Sprintf("instance %d", index+1)))
	return server, trigger
}

// The streaming method is offered, as a unary method on a service of its own that
// registers it.
func TestNewOffersATriggerAsASubscription(t *testing.T) {
	server, _ := newTestServer(t)

	c, ok := server.calls[subscriptionKey("Execute")]
	require.True(t, ok, "the streaming method should be offered as a subscription")

	assert.True(t, c.subscribe)
	assert.Equal(t, "Execute", c.method, "what is registered is the trigger's own method")
	assert.Equal(t, string(testService().FullName()), c.service, "and its own service, not the synthetic one")
	assert.Equal(t, "*pb.CapabilityRequest", c.input.String(), "the form draws the trigger's real input")
	assert.Equal(t, "*emptypb.Empty", c.output.String(), "registering answers nothing")
	assert.Equal(t, "*pb.CapabilityResponse", c.event.String(), "the event is what the trigger streams")
}

// Invoking the synthetic method registers the trigger, under the trigger ID the
// request carried.
func TestInvokeSubscribes(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	server, trigger := newSubscribableServer(t, &Fleet{}, hub, 2)

	md, err := server.methodDescriptor("Execute")
	require.NoError(t, err)
	args := dynamic.NewMessage(md.GetInputType())
	reply := dynamic.NewMessage(md.GetOutputType())

	ctx := metadata.NewOutgoingContext(t.Context(), metadata.New(map[string]string{
		TriggerIDHeader: "ui-trigger-named",
	}))
	require.NoError(t, server.Invoke(ctx, "/"+subscriptionKey("Execute"), args, reply))

	requests := trigger.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, "ui-trigger-named", requests[0].TriggerID)
	assert.Equal(t, "Execute", requests[0].Method, "the trigger's method, not the synthetic one")
	require.NotNil(t, requests[0].Payload)

	// And it is the instance that was mounted, so an event says which node it came
	// from.
	subscriptions := hub.List()
	require.Len(t, subscriptions, 1)
	require.Len(t, subscriptions[0].Instances, 1)
	assert.Equal(t, 2, subscriptions[0].Instances[0].Instance)
	assert.Equal(t, "instance 3", subscriptions[0].Instances[0].Label)
}

// A caller that names no trigger ID gets one, so a subscription always has an
// identity even when nobody chose it.
func TestInvokeMintsATriggerID(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	server, trigger := newSubscribableServer(t, &Fleet{}, hub, 0)

	md, err := server.methodDescriptor("Execute")
	require.NoError(t, err)
	require.NoError(t, server.Invoke(t.Context(), "/"+subscriptionKey("Execute"),
		dynamic.NewMessage(md.GetInputType()), dynamic.NewMessage(md.GetOutputType())))

	requests := trigger.requests()
	require.Len(t, requests, 1)
	assert.True(t, strings.HasPrefix(requests[0].TriggerID, triggerIDPrefix), requests[0].TriggerID)
	require.Len(t, hub.List(), 1)
	assert.Equal(t, requests[0].TriggerID, hub.List()[0].TriggerID)
}

// A Server that was never mounted has nowhere to put a subscription, and says so
// as its own failure rather than the caller's.
func TestSubscribingWithoutAHubIsASystemError(t *testing.T) {
	server, _ := newTestServer(t)

	md, err := server.methodDescriptor("Execute")
	require.NoError(t, err)

	err = server.Invoke(t.Context(), "/"+subscriptionKey("Execute"),
		dynamic.NewMessage(md.GetInputType()), dynamic.NewMessage(md.GetOutputType()))
	require.Error(t, err)
	assert.False(t, isUserError(err), "%v", err)
	assert.Equal(t, http.StatusInternalServerError, httpStatus(err))
}

// One fan-out is one subscription: every instance is registered under the same
// trigger ID, so what the user asked for as one table is one table.
func TestFanoutSubscribesEveryInstanceUnderOneTriggerID(t *testing.T) {
	const instances = 4

	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	triggers := make([]*fakeTrigger, 0, instances)
	var first *Server
	for i := range instances {
		server, trigger := newSubscribableServer(t, fleet, hub, i)
		triggers = append(triggers, trigger)
		if i == 0 {
			first = server
		}
	}

	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: first}

	targets := make([]int, 0, instances)
	for i := range instances {
		targets = append(targets, i)
	}

	response := f.run(fanoutRequest{
		Method: strings.ReplaceAll(subscriptionKey("Execute"), "/", "."),
		Groups: []requestGroup{{Instances: targets, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	}, subscribeHeader(t, "ui-trigger-fanout"))

	require.Len(t, response.Results, instances)
	for _, row := range response.Results {
		assert.Equal(t, "ok", row.Status, "instance %d: %s", row.Instance, row.Error)
	}

	// One subscription, with a column per instance.
	subscriptions := hub.List()
	require.Len(t, subscriptions, 1)
	assert.Equal(t, "ui-trigger-fanout", subscriptions[0].TriggerID)
	assert.Len(t, subscriptions[0].Instances, instances)

	// And each instance was told the same trigger ID.
	for i, trigger := range triggers {
		requests := trigger.requests()
		require.Len(t, requests, 1, "instance %d", i)
		assert.Equal(t, "ui-trigger-fanout", requests[0].TriggerID, "instance %d", i)
	}
}

// The endpoint settles the trigger ID before dispatching, and reports it: a page
// that named none still has to know which subscription to watch.
func TestFanoutEndpointSettlesAndReportsTheTriggerID(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	server, trigger := newSubscribableServer(t, fleet, hub, 0)

	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: server}

	body, err := json.Marshal(fanoutRequest{
		Method: strings.ReplaceAll(subscriptionKey("Execute"), "/", "."),
		Groups: []requestGroup{{Instances: []int{0}, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	f.invoke(recorder, csrfPost(DefaultPrefix+"/request/fanout", body))

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response fanoutResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.NotEmpty(t, response.TriggerID, "the page needs it to open the stream")

	requests := trigger.requests()
	require.Len(t, requests, 1)
	assert.Equal(t, response.TriggerID, requests[0].TriggerID)
}

// The stream opens with the table and then carries each event as it arrives.
func TestStreamSendsASnapshotThenRows(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	server, trigger := newSubscribableServer(t, fleet, hub, 0)
	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: server}

	_, err := join(t, hub, "ui-trigger-stream", 0, trigger)
	require.NoError(t, err)

	// One event before the reader arrives, so the snapshot has something in it.
	trigger.deliver(t, 0, event(t, "event-before", "first"))
	s, err := hub.get("ui-trigger-stream")
	require.NoError(t, err)
	eventually(t, "the first event should be recorded", func() bool { return len(s.rows()) == 1 })

	request := httptest.NewRequest(http.MethodGet,
		DefaultPrefix+"/request/subscriptions/stream?trigger=ui-trigger-stream", nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request = request.WithContext(ctx)

	recorder := newSyncRecorder()
	served := make(chan struct{})
	go func() {
		defer close(served)
		f.stream(recorder, request)
	}()

	eventually(t, "the snapshot should be written", func() bool {
		return strings.Contains(recorder.String(), MessageSnapshot)
	})
	assert.Contains(t, recorder.String(), "event-before")
	assert.Equal(t, "text/event-stream", recorder.contentType())

	// And a live one arrives on the open stream.
	trigger.deliver(t, 0, event(t, "event-after", "second"))
	eventually(t, "the live event should reach the reader", func() bool {
		return strings.Contains(recorder.String(), "event-after")
	})

	cancel()
	select {
	case <-served:
	case <-time.After(2 * time.Second):
		t.Fatal("the stream did not end when the reader went away")
	}
}

// Closing the window closes the subscription: the stream ending is the last
// reader leaving, and nobody coming back within the grace period unregisters it.
func TestClosingTheStreamEventuallyUnregisters(t *testing.T) {
	hub := NewHub()
	hub.grace = 20 * time.Millisecond
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	server, trigger := newSubscribableServer(t, fleet, hub, 0)
	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: server}

	_, err := join(t, hub, "ui-trigger-window", 0, trigger)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet,
		DefaultPrefix+"/request/subscriptions/stream?trigger=ui-trigger-window", nil).WithContext(ctx)

	served := make(chan struct{})
	go func() {
		defer close(served)
		f.stream(newSyncRecorder(), request)
	}()

	s, err := hub.get("ui-trigger-window")
	require.NoError(t, err)
	eventually(t, "the reader should attach", func() bool { return s.status(false).Readers == 1 })

	cancel()
	<-served

	eventually(t, "the trigger should be unregistered once nobody comes back", func() bool {
		return len(trigger.unregisters()) == 1
	})
}

// The sidebar lists what is running.
func TestSubscriptionsEndpointListsThem(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	server, trigger := newSubscribableServer(t, fleet, hub, 0)
	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: server}

	_, err := join(t, hub, "ui-trigger-listed", 0, trigger)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	f.subscriptions(recorder, httptest.NewRequest(http.MethodGet, DefaultPrefix+"/request/subscriptions", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var body struct {
		Subscriptions []Status `json:"subscriptions"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Len(t, body.Subscriptions, 1)
	assert.Equal(t, "ui-trigger-listed", body.Subscriptions[0].TriggerID)
}

// Acknowledging and closing both reach the capability, and both require the CSRF
// token, since both reach a capability.
func TestAckAndCloseEndpoints(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	server, trigger := newSubscribableServer(t, fleet, hub, 0)
	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: server}

	_, err := join(t, hub, "ui-trigger-commands", 0, trigger)
	require.NoError(t, err)

	s, err := hub.get("ui-trigger-commands")
	require.NoError(t, err)
	trigger.deliver(t, 0, event(t, "event-1", "hello"))
	eventually(t, "the event should be recorded", func() bool { return len(s.rows()) == 1 })

	t.Run("without a token nothing happens", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		f.ack(recorder, httptest.NewRequest(http.MethodPost, DefaultPrefix+"/request/subscriptions/ack",
			strings.NewReader(`{"triggerId":"ui-trigger-commands","eventId":"event-1"}`)))
		assert.Equal(t, http.StatusUnauthorized, recorder.Code)
		assert.Empty(t, trigger.acks())
	})

	t.Run("ack reaches the capability", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		f.ack(recorder, csrfPost(DefaultPrefix+"/request/subscriptions/ack",
			[]byte(`{"triggerId":"ui-trigger-commands","eventId":"event-1"}`)))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		assert.Equal(t, []string{"ui-trigger-commands/event-1/Execute"}, trigger.acks())
	})

	t.Run("close unregisters", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		f.unsubscribe(recorder, csrfPost(DefaultPrefix+"/request/subscriptions/close",
			[]byte(`{"triggerId":"ui-trigger-commands"}`)))
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		assert.Len(t, trigger.unregisters(), 1)
		assert.Empty(t, hub.List())
	})
}

// The page can ask for a trigger ID before it subscribes, so it knows which
// subscription to watch before the first event can arrive.
func TestTriggerIDEndpointMintsOne(t *testing.T) {
	f := &fanout{hub: NewHub(), prefix: DefaultPrefix}

	recorder := httptest.NewRecorder()
	f.triggerID(recorder, httptest.NewRequest(http.MethodGet, DefaultPrefix+"/request/trigger-id", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var body struct {
		TriggerID string `json:"triggerId"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	assert.True(t, strings.HasPrefix(body.TriggerID, triggerIDPrefix), body.TriggerID)
}

// syncRecorder is a response the test can read while the handler is still
// writing it.
//
// httptest.ResponseRecorder cannot be: its body is a bytes.Buffer, and a stream
// is served on a goroutine of its own while the test watches what has arrived. A
// guarded one keeps that the test's business rather than making the stream lock
// something for its benefit.
type syncRecorder struct {
	mu     sync.Mutex
	header http.Header
	body   strings.Builder
	code   int
}

func newSyncRecorder() *syncRecorder {
	return &syncRecorder{header: http.Header{}, code: http.StatusOK}
}

func (r *syncRecorder) Header() http.Header { return r.header }

func (r *syncRecorder) WriteHeader(code int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.code = code
}

func (r *syncRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.Write(p)
}

// Flush is what makes this a streaming response as far as the handler is
// concerned. Nothing to do: the buffer is already the whole of it.
func (r *syncRecorder) Flush() {}

func (r *syncRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

// contentType is read under the lock, so the header the handler set before its
// first write is safely visible.
func (r *syncRecorder) contentType() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.header.Get("Content-Type")
}

// subscribeHeader is the header a fan-out sends: settled metadata plus the trigger
// ID every instance is to register under.
func subscribeHeader(t *testing.T, triggerID string) http.Header {
	t.Helper()
	header := settle(t, nil)
	header.Set(TriggerIDHeader, triggerID)
	return header
}

// csrfPost is a POST carrying the token the command endpoints require.
func csrfPost(path string, body []byte) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	request.Header.Set(fanoutHeaderName, "a-token")
	request.AddCookie(&http.Cookie{Name: fanoutCookieName, Value: "a-token"})
	return request
}

// A registration every instance refused is not a subscription: nothing is
// watching anything, so the endpoint names no trigger ID and the sidebar - which
// is the hub's list - has nothing in it. A page told otherwise would show a row
// for a subscription that does not exist, and offer to close it.
func TestFanoutReportsNoTriggerIDWhenNobodySubscribed(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	server, trigger := newSubscribableServer(t, fleet, hub, 0)
	trigger.registerErr = errors.New("the workflow was not accepted")

	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: server}

	body, err := json.Marshal(fanoutRequest{
		Method: strings.ReplaceAll(subscriptionKey("Execute"), "/", "."),
		Groups: []requestGroup{{Instances: []int{0}, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	f.invoke(recorder, csrfPost(DefaultPrefix+"/request/fanout", body))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response fanoutResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	require.Len(t, response.Results, 1)
	assert.Empty(t, response.TriggerID, "there is no subscription to watch")
	assert.Empty(t, hub.List(), "and none for the sidebar to list")
}

// One instance taking it is a subscription, though: the trigger ID is reported so
// the reader can watch what did register, and the instance that refused says so
// on its own row.
func TestFanoutReportsTheTriggerIDWhenOneInstanceSubscribed(t *testing.T) {
	hub := NewHub()
	t.Cleanup(func() { _ = hub.Close() })

	fleet := &Fleet{}
	first, _ := newSubscribableServer(t, fleet, hub, 0)
	_, refusing := newSubscribableServer(t, fleet, hub, 1)
	refusing.registerErr = errors.New("the workflow was not accepted")

	f := &fanout{fleet: fleet, hub: hub, prefix: DefaultPrefix, uiPath: DefaultPrefix + "/ui", server: first}

	body, err := json.Marshal(fanoutRequest{
		Method: strings.ReplaceAll(subscriptionKey("Execute"), "/", "."),
		Groups: []requestGroup{{Instances: []int{0, 1}, Body: []byte(`{"metadata":[],"data":[{}]}`)}},
	})
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	f.invoke(recorder, csrfPost(DefaultPrefix+"/request/fanout", body))
	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())

	var response fanoutResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))

	assert.NotEmpty(t, response.TriggerID)
	require.Len(t, hub.List(), 1)
	assert.Len(t, hub.List()[0].Instances, 1, "only the instance that took it is attached")
}
