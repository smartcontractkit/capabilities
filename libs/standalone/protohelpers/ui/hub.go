package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// A trigger is not a call, so it does not fit the request-and-response the rest
// of the page is: it is registered once and then delivers whatever it delivers,
// for as long as it is registered, to however many instances registered it.
//
// The Hub is what holds that. A subscription is keyed by its trigger ID, which is
// the identifier the registration carried, so registering the same trigger ID on
// another instance later joins the subscription already running rather than
// starting a second one - which is what makes "send to two instances now, a third
// in a minute" one table rather than two.
//
// Within a subscription an event is keyed by its own ID, and every instance that
// delivered it is a column. Instances are meant to agree; when they do not, that
// is the thing worth seeing, so the payloads are hashed and a row that carries
// more than one hash is marked rather than averaged away.

const (
	// DefaultGrace is how long a subscription outlives the last reader watching
	// it. Closing the window is how a subscription is closed, and a reload closes
	// the window - so the two are told apart by waiting to see whether anyone
	// comes back.
	DefaultGrace = time.Minute

	// DefaultRing is how many events a subscription keeps. A reader that
	// reattaches is sent them, so the table it left is the table it returns to.
	DefaultRing = 200

	// clientBuffer is how far behind a reader may fall before it is dropped.
	// Dropping is safe: the browser reconnects and is sent the whole table, which
	// is more correct than a reader that has silently missed a row.
	clientBuffer = 64
)

// Hub holds every live subscription of the process, keyed by trigger ID.
//
// One Hub is shared by every instance, the same way the Fleet is: an embed run's
// instances are separate registries but one browser, so a subscription registered
// across four of them has to be one thing for the page to show it as one table.
type Hub struct {
	grace time.Duration
	ring  int

	// ctx outlives the request that registered a trigger. A registration is not
	// the request's to own: the request returns as soon as the trigger is
	// registered, and the events arrive long afterwards.
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	subs map[string]*subscription
}

// NewHub builds an empty Hub with the default grace and ring.
func NewHub() *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		grace:  DefaultGrace,
		ring:   DefaultRing,
		ctx:    ctx,
		cancel: cancel,
		subs:   map[string]*subscription{},
	}
}

// Close unregisters every subscription and stops reading them.
func (h *Hub) Close() error {
	h.mu.Lock()
	subs := make([]*subscription, 0, len(h.subs))
	for _, s := range h.subs {
		subs = append(subs, s)
	}
	h.subs = map[string]*subscription{}
	h.mu.Unlock()

	var errs []error
	for _, s := range subs {
		errs = append(errs, s.close())
	}
	h.cancel()
	return errors.Join(errs...)
}

// registration is one instance joining one subscription.
type registration struct {
	triggerID    string
	capabilityID string
	// service is the real service the trigger belongs to, for the page to show.
	service string
	method  string

	instance int
	label    string
	trigger  capabilities.TriggerExecutable

	metadata capabilities.RequestMetadata
	payload  *anypb.Any

	// eventType is the generated Go type of the trigger's streamed message, which
	// is how a delivered event is read out of the Any it arrives in.
	eventType reflect.Type
}

// subscribe registers one instance's trigger, joining the subscription with this
// trigger ID or starting it.
func (h *Hub) subscribe(r registration) (*Status, error) {
	s, err := h.subscription(r)
	if err != nil {
		return nil, err
	}

	if err = s.attach(r); err != nil {
		// A subscription nobody managed to attach to is not left behind: it would
		// sit in the sidebar claiming to be watching something.
		h.dropIfEmpty(s)
		return nil, err
	}

	status := s.status(false)
	return &status, nil
}

// subscription finds the one this registration belongs to, or starts it.
//
// A trigger ID names a subscription, so one that already exists has to be the
// same trigger: joining "the cron I started" with a different method would put
// two unrelated streams in one table.
func (h *Hub) subscription(r registration) (*subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if existing, ok := h.subs[r.triggerID]; ok {
		if existing.capabilityID != r.capabilityID || existing.method != r.method {
			return nil, userErrorf(
				"trigger ID %s is already subscribed to %s/%s, so it cannot also subscribe to %s/%s - use a different trigger ID",
				r.triggerID, existing.capabilityID, existing.method, r.capabilityID, r.method)
		}
		return existing, nil
	}

	s := &subscription{
		hub:          h,
		triggerID:    r.triggerID,
		capabilityID: r.capabilityID,
		service:      r.service,
		method:       r.method,
		workflowID:   r.metadata.WorkflowID,
		eventType:    r.eventType,
		created:      time.Now().UTC(),
		attached:     map[int]*attachment{},
		events:       map[string]*Row{},
		clients:      map[*client]struct{}{},
	}
	// The grace clock starts now, not when the first reader leaves: a subscription
	// nobody ever watches is one nobody is going to close.
	s.startGrace()

	h.subs[r.triggerID] = s
	return s, nil
}

// dropIfEmpty removes a subscription nothing is attached to.
func (h *Hub) dropIfEmpty(s *subscription) {
	s.mu.Lock()
	empty := len(s.attached) == 0
	s.mu.Unlock()
	if !empty {
		return
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[s.triggerID] == s {
		delete(h.subs, s.triggerID)
	}
}

// Live reports whether this trigger ID names a subscription that exists.
//
// It is what the fan-out asks before telling a page which subscription it just
// started: a registration can fail while the call that carried it succeeds - the
// capability answers with an error rather than the transport failing - so
// "the request went through" is not the same question as "is anything watching".
func (h *Hub) Live(triggerID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.subs[triggerID]
	return ok
}

// get is the subscription with this trigger ID.
func (h *Hub) get(triggerID string) (*subscription, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.subs[triggerID]
	if !ok {
		return nil, userErrorf("no subscription with trigger ID %s", triggerID)
	}
	return s, nil
}

// List is every live subscription, newest last, for the page's sidebar.
func (h *Hub) List() []Status {
	h.mu.Lock()
	subs := make([]*subscription, 0, len(h.subs))
	for _, s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()

	sort.Slice(subs, func(i, j int) bool { return subs[i].created.Before(subs[j].created) })

	out := make([]Status, 0, len(subs))
	for _, s := range subs {
		out = append(out, s.status(false))
	}
	return out
}

// Unsubscribe closes a subscription, or detaches the instances named.
func (h *Hub) Unsubscribe(triggerID string, instances []int) error {
	s, err := h.get(triggerID)
	if err != nil {
		return err
	}

	if len(instances) == 0 {
		h.remove(s)
		return s.close()
	}

	var errs []error
	for _, index := range instances {
		errs = append(errs, s.detach(index))
	}
	h.dropIfEmpty(s)
	return errors.Join(errs...)
}

// Ack forwards a reader's acknowledgement to every instance that delivered the
// event.
//
// The page acks rather than this doing it on delivery: a capability that redelivers
// what was not acknowledged is doing what it is meant to, and acknowledging an
// event the browser has not been shown would hide exactly the delivery being
// debugged.
func (h *Hub) Ack(triggerID, eventID string) error {
	s, err := h.get(triggerID)
	if err != nil {
		return err
	}
	return s.ack(h.ctx, eventID)
}

// remove takes a subscription out of the hub without closing it.
func (h *Hub) remove(s *subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs[s.triggerID] == s {
		delete(h.subs, s.triggerID)
	}
}

// subscription is one trigger ID: the instances registered under it, the events
// they delivered, and the readers watching.
type subscription struct {
	hub *Hub

	triggerID    string
	capabilityID string
	service      string
	method       string
	workflowID   string
	eventType    reflect.Type
	created      time.Time

	mu         sync.Mutex
	attached   map[int]*attachment
	events     map[string]*Row
	order      []string
	clients    map[*client]struct{}
	graceTimer *time.Timer
	closed     bool
}

// attachment is one instance's registration, kept because unregistering takes the
// request that registered.
type attachment struct {
	instance int
	label    string
	trigger  capabilities.TriggerExecutable
	request  capabilities.TriggerRegistrationRequest
	stop     context.CancelFunc
}

// attach registers the trigger on one instance and starts reading its events.
func (s *subscription) attach(r registration) error {
	request := capabilities.TriggerRegistrationRequest{
		TriggerID: r.triggerID,
		Metadata:  r.metadata,
		Method:    r.method,
		Payload:   r.payload,
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return userErrorf("subscription %s has been closed", s.triggerID)
	}
	if _, taken := s.attached[r.instance]; taken {
		s.mu.Unlock()
		return userErrorf("%s is already subscribed to trigger ID %s", r.label, s.triggerID)
	}
	s.mu.Unlock()

	// Registered outside the lock: this reaches the capability, and holding the
	// subscription's lock across it would stall every reader of every event while
	// one instance registers.
	ctx, stop := context.WithCancel(s.hub.ctx)
	events, err := r.trigger.RegisterTrigger(ctx, request)
	if err != nil {
		stop()
		return fromCapability(err)
	}

	a := &attachment{instance: r.instance, label: r.label, trigger: r.trigger, request: request, stop: stop}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		stop()
		return userErrorf("subscription %s has been closed", s.triggerID)
	}
	if _, taken := s.attached[r.instance]; taken {
		// Lost a race with another registration of the same instance. Undone rather
		// than left registered twice.
		s.mu.Unlock()
		stop()
		_ = r.trigger.UnregisterTrigger(s.hub.ctx, request)
		return userErrorf("%s is already subscribed to trigger ID %s", r.label, s.triggerID)
	}
	s.attached[r.instance] = a
	s.mu.Unlock()

	go s.read(ctx, a, events)
	s.broadcast(Message{Type: MessageAttached, TriggerID: s.triggerID, Status: ptr(s.status(false))})
	return nil
}

// detach unregisters one instance and stops reading it.
func (s *subscription) detach(instance int) error {
	s.mu.Lock()
	a, ok := s.attached[instance]
	if ok {
		delete(s.attached, instance)
	}
	s.mu.Unlock()

	if !ok {
		return userErrorf("instance %d is not subscribed to trigger ID %s", instance, s.triggerID)
	}

	a.stop()
	err := a.trigger.UnregisterTrigger(s.hub.ctx, a.request)
	s.broadcast(Message{Type: MessageAttached, TriggerID: s.triggerID, Status: ptr(s.status(false))})
	if err != nil {
		return fromCapability(err)
	}
	return nil
}

// close unregisters every instance and tells every reader the subscription is over.
func (s *subscription) close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	if s.graceTimer != nil {
		s.graceTimer.Stop()
		s.graceTimer = nil
	}
	attached := make([]*attachment, 0, len(s.attached))
	for _, a := range s.attached {
		attached = append(attached, a)
	}
	s.attached = map[int]*attachment{}
	s.mu.Unlock()

	var errs []error
	for _, a := range attached {
		a.stop()
		if err := a.trigger.UnregisterTrigger(s.hub.ctx, a.request); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.label, err))
		}
	}

	s.broadcast(Message{Type: MessageClosed, TriggerID: s.triggerID, Status: ptr(s.status(true))})
	s.disconnectAll()
	return errors.Join(errs...)
}

// read forwards one instance's events until the channel closes or the attachment
// is stopped.
func (s *subscription) read(ctx context.Context, a *attachment, events <-chan capabilities.TriggerResponse) {
	for {
		select {
		case <-ctx.Done():
			return
		case response, open := <-events:
			if !open {
				return
			}
			s.record(a, response)
		}
	}
}

// record merges one instance's delivery of one event into its row.
func (s *subscription) record(a *attachment, response capabilities.TriggerResponse) {
	node := Node{
		Instance: a.instance,
		Label:    a.label,
		At:       time.Now().UTC(),
	}
	if response.Err != nil {
		node.Error = response.Err.Error()
	}

	payload, id, err := s.decode(response.Event)
	if err != nil && node.Error == "" {
		node.Error = err.Error()
	}
	if err == nil {
		node.PayloadID = id
	}

	// An event with no ID cannot be a row of its own without every delivery
	// looking like a separate event, so it is keyed by what it carried instead.
	key := response.Event.ID
	if key == "" {
		key = "(no event ID) " + id
	}

	row := s.merge(key, node, payload)
	if row != nil {
		s.broadcast(Message{Type: MessageRow, TriggerID: s.triggerID, Row: row})
	}
}

// decode reads a delivered event out of the Any it arrives in, as the generated Go
// type the trigger declares, and hashes it.
//
// The hash is what makes disagreement visible: identical payloads hash the same,
// so a row with one hash is every instance agreeing and a row with two is not.
// Marshalling is deterministic so that a map field cannot make two equal payloads
// look different.
func (s *subscription) decode(event capabilities.TriggerEvent) (json.RawMessage, string, error) {
	if event.Payload == nil {
		if event.Outputs != nil {
			// The values.Map path, which only a DAG registration takes. This
			// package always registers with a payload, so seeing one means the
			// capability answered a request it was not sent.
			return nil, "", fmt.Errorf("the event carried Outputs rather than a Payload")
		}
		return nil, "", fmt.Errorf("the event carried no payload")
	}

	message, ok := reflect.New(s.eventType.Elem()).Interface().(proto.Message)
	if !ok {
		return nil, "", fmt.Errorf("%s is not a protobuf message", s.eventType)
	}
	if err := event.Payload.UnmarshalTo(message); err != nil {
		return nil, "", fmt.Errorf("failed to read the event as %T: %w", message, err)
	}

	// Proto names, which is the spelling the response tab already uses, so an
	// event and a response read the same way.
	encoded, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return nil, "", fmt.Errorf("failed to encode the event: %w", err)
	}

	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, "", fmt.Errorf("failed to hash the event: %w", err)
	}
	return encoded, shortHash(canonical), nil
}

// merge folds a node into its row and returns the row to send, or nil if the
// subscription is closed.
func (s *subscription) merge(id string, node Node, payload json.RawMessage) *Row {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	row, ok := s.events[id]
	if !ok {
		row = &Row{ID: id, First: node.At}
		s.events[id] = row
		s.order = append(s.order, id)
		s.trim()
	}

	row.add(node, payload)

	copied := row.clone()
	return &copied
}

// trim keeps the kept-event count at the ring size. Called with the lock held.
func (s *subscription) trim() {
	for len(s.order) > s.hub.ring {
		delete(s.events, s.order[0])
		s.order = s.order[1:]
	}
}

// rows is every kept event, oldest first. Copied, so a reader is never handed
// something another delivery is about to change under it.
func (s *subscription) rows() []Row {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Row, 0, len(s.order))
	for _, id := range s.order {
		if row, ok := s.events[id]; ok {
			out = append(out, row.clone())
		}
	}
	return out
}

// status describes the subscription, with its events when withRows.
func (s *subscription) status(closed bool) Status {
	s.mu.Lock()
	instances := make([]Attached, 0, len(s.attached))
	for _, a := range s.attached {
		instances = append(instances, Attached{Instance: a.instance, Label: a.label})
	}
	events := len(s.order)
	readers := len(s.clients)
	graced := s.graceTimer != nil
	if s.closed {
		closed = true
	}
	s.mu.Unlock()

	sort.Slice(instances, func(i, j int) bool { return instances[i].Instance < instances[j].Instance })

	return Status{
		TriggerID:    s.triggerID,
		CapabilityID: s.capabilityID,
		Service:      s.service,
		Method:       s.method,
		WorkflowID:   s.workflowID,
		Instances:    instances,
		Events:       events,
		Readers:      readers,
		InGrace:      graced && readers == 0,
		Closed:       closed,
		Created:      s.created,
	}
}

// ack forwards an acknowledgement to every instance that delivered the event.
func (s *subscription) ack(ctx context.Context, eventID string) error {
	s.mu.Lock()
	row, ok := s.events[eventID]
	var targets []*attachment
	if ok {
		for _, node := range row.Nodes {
			if a, attached := s.attached[node.Instance]; attached {
				targets = append(targets, a)
			}
		}
	}
	s.mu.Unlock()

	if !ok {
		return userErrorf("trigger ID %s has no event %s", s.triggerID, eventID)
	}

	var errs []error
	for _, a := range targets {
		if err := a.trigger.AckEvent(ctx, s.triggerID, eventID, s.method); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", a.label, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fromCapability(err)
	}
	return nil
}

func ptr[T any](v T) *T { return &v }
