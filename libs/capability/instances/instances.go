package instances

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

type Result[T any] struct {
	Value T
	Err   error
}

type MismatchError[T any] struct {
	Results []Result[T]
	equal   func(a, b T) bool
}

func (e *MismatchError[T]) Error() string {
	var b strings.Builder
	b.WriteString("instances disagree")
	first := e.Results[0]
	for i, r := range e.Results[1:] {
		if !sameError(first.Err, r.Err) {
			_, _ = fmt.Fprintf(&b, "\ninstance %d: error %s, instance 0: error %s", i+1, describeError(r.Err), describeError(first.Err))
			continue
		}
		if !e.equal(first.Value, r.Value) {
			_, _ = fmt.Fprintf(&b, "\ninstance %d: %v, instance 0: %v", i+1, r.Value, first.Value)
		}
	}
	return b.String()
}

// Event is one trigger event the instances agreed on, or Mismatch when they did not.
type Event struct {
	capabilities.TriggerResponse
	Mismatch *MismatchError[capabilities.TriggerResponse]
}

// Execute calls Execute on every instance and returns the response they agree on. It returns a *MismatchError
// when the instances disagree, on the response or the error.
func Execute[C capabilities.Executable](
	ctx context.Context,
	cs []C,
	request capabilities.CapabilityRequest) (capabilities.CapabilityResponse, error) {
	results, err := each(cs, func(c C) (capabilities.CapabilityResponse, error) {
		return c.Execute(ctx, cloneCapabilityRequest(request))
	})
	if err != nil {
		return capabilities.CapabilityResponse{}, err
	}
	return agree(results, func(a, b capabilities.CapabilityResponse) bool {
		return samePayload(a.Payload, b.Payload) && slices.Equal(a.Metadata.Metering, b.Metadata.Metering)
	})
}

var errNoEvent = errors.New("no event")

// RegisterTrigger registers the trigger on every instance and returns the events they agree on, matched by
// event ID in whatever order each instance sends them. An event they disagree on carries a Mismatch, as does
// one some instance never sent by the time every instance's channel has closed. It returns a *MismatchError when
// the instances disagree on whether registering failed.
func RegisterTrigger[C capabilities.TriggerExecutable](ctx context.Context, cs []C, request capabilities.TriggerRegistrationRequest) (<-chan Event, error) {
	results, err := each(cs, func(c C) (<-chan capabilities.TriggerResponse, error) {
		return c.RegisterTrigger(ctx, cloneTriggerRequest(request))
	})

	if err != nil {
		return nil, err
	}

	if _, err = agree(results, func(_, _ <-chan capabilities.TriggerResponse) bool { return true }); err != nil {
		return nil, errors.Join(err, unregisterRegistered(ctx, cs, results, request))
	}

	out := make(chan Event)
	go func() {
		defer close(out)
		matchEvents(ctx, fanIn(ctx, results), len(results), out)
	}()
	return out, nil
}

func matchEvents(ctx context.Context, in <-chan instanceResponse, instances int, out chan<- Event) {
	seen := map[string]map[int]capabilities.TriggerResponse{}
	emitted := map[string]bool{}
	emit := func(id string) bool {
		emitted[id] = true
		defer delete(seen, id)
		return sendOrDone(ctx, out, toEvent(seen[id], instances))
	}

	for r := range in {
		id := r.response.Event.ID
		if emitted[id] {
			continue
		}

		if seen[id] == nil {
			seen[id] = map[int]capabilities.TriggerResponse{}
		}

		seen[id][r.instance] = withDuplicate(seen[id], r)
		if len(seen[id]) == instances && !emit(id) {
			return
		}
	}

	for _, id := range slices.Sorted(maps.Keys(seen)) {
		if !emit(id) {
			return
		}
	}
}

type instanceResponse struct {
	instance int
	response capabilities.TriggerResponse
}

func withDuplicate(byInstance map[int]capabilities.TriggerResponse, r instanceResponse) capabilities.TriggerResponse {
	held, dup := byInstance[r.instance]
	if !dup || sameResponse(held, r.response) {
		return r.response
	}
	return capabilities.TriggerResponse{
		Event: capabilities.TriggerEvent{ID: r.response.Event.ID},
		Err:   fmt.Errorf("instance %d sent event %q twice, differently", r.instance, r.response.Event.ID),
	}
}

func sameResponse(a, b capabilities.TriggerResponse) bool {
	return sameError(a.Err, b.Err) && sameEvent(a, b)
}

func fanIn(ctx context.Context, results []Result[<-chan capabilities.TriggerResponse]) <-chan instanceResponse {
	merged := make(chan instanceResponse)
	var wg sync.WaitGroup
	for i, r := range results {
		wg.Go(func() {
			for {
				select {
				case response, ok := <-r.Value:
					if !ok || !sendOrDone(ctx, merged, instanceResponse{instance: i, response: response}) {
						return
					}
				case <-ctx.Done():
					return
				}
			}
		})
	}
	go func() {
		wg.Wait()
		close(merged)
	}()
	return merged
}

func sendOrDone[T any](ctx context.Context, ch chan<- T, v T) bool {
	select {
	case ch <- v:
		return true
	case <-ctx.Done():
		return false
	}
}

func toEvent(byInstance map[int]capabilities.TriggerResponse, instances int) Event {
	events := make([]Result[capabilities.TriggerResponse], instances)
	for i := range events {
		response, sent := byInstance[i]
		events[i] = Result[capabilities.TriggerResponse]{Value: response, Err: response.Err}
		if !sent {
			events[i].Err = errNoEvent
		}
	}

	if len(byInstance) < instances {
		return Event{Mismatch: &MismatchError[capabilities.TriggerResponse]{Results: events, equal: sameEvent}}
	}
	if m := mismatch(events, sameEvent); m != nil {
		return Event{Mismatch: m}
	}
	return Event{TriggerResponse: events[0].Value}
}

func sameEvent(a, b capabilities.TriggerResponse) bool {
	return a.Event.ID == b.Event.ID && samePayload(a.Event.Payload, b.Event.Payload)
}

func unregisterRegistered[C capabilities.TriggerExecutable](
	ctx context.Context, cs []C, results []Result[<-chan capabilities.TriggerResponse], request capabilities.TriggerRegistrationRequest,
) error {
	var errs []error
	for i, r := range results {
		if r.Err == nil {
			if err := cs[i].UnregisterTrigger(ctx, cloneTriggerRequest(request)); err != nil {
				errs = append(errs, fmt.Errorf("instance %d: failed to roll back registration: %w", i, err))
			}
		}
	}
	return errors.Join(errs...)
}

// UnregisterTrigger unregisters the trigger on every instance.
// It returns a *MismatchError when the instances disagree on whether it failed.
func UnregisterTrigger[C capabilities.TriggerExecutable](ctx context.Context, cs []C, request capabilities.TriggerRegistrationRequest) error {
	results, err := each(cs, func(c C) (struct{}, error) {
		return struct{}{}, c.UnregisterTrigger(ctx, cloneTriggerRequest(request))
	})
	if err != nil {
		return err
	}
	_, err = agree(results, func(_, _ struct{}) bool { return true })
	return err
}

// TypedEvent is an Event with its payload unpacked. Err is the error the instances agreed on, or why the
// payload could not be unpacked.
type TypedEvent[O proto.Message] struct {
	capabilities.TriggerAndId[O]
	Err      error
	Mismatch *MismatchError[capabilities.TriggerResponse]
}

// TypedExecute is Execute for method, with input packed and the agreed response unpacked.
func TypedExecute[O proto.Message, C capabilities.Executable, I proto.Message](
	ctx context.Context, cs []C, method string, metadata capabilities.RequestMetadata, input I,
) (*capabilities.ResponseAndMetadata[O], error) {
	payload, err := anypb.New(input)
	if err != nil {
		return nil, err
	}
	response, err := Execute(ctx, cs, capabilities.CapabilityRequest{Method: method, Metadata: metadata, Payload: payload})
	if err != nil {
		return nil, err
	}

	output := newMessage[O]()
	if err = response.Payload.UnmarshalTo(output); err != nil {
		return nil, err
	}
	return &capabilities.ResponseAndMetadata[O]{Response: output, ResponseMetadata: response.Metadata, OCRAttestation: response.OCRAttestation}, nil
}

// TypedRegisterTrigger is RegisterTrigger for method, with input packed and each agreed event unpacked.
func TypedRegisterTrigger[O proto.Message, C capabilities.TriggerExecutable, I proto.Message](
	ctx context.Context, cs []C, method, triggerID string, metadata capabilities.RequestMetadata, input I,
) (<-chan TypedEvent[O], error) {
	request, err := triggerRequest(method, triggerID, metadata, input)
	if err != nil {
		return nil, err
	}
	events, err := RegisterTrigger(ctx, cs, request)
	if err != nil {
		return nil, err
	}

	out := make(chan TypedEvent[O])
	go func() {
		defer close(out)
		for e := range events {
			typed := TypedEvent[O]{Err: e.Err, Mismatch: e.Mismatch}
			typed.Id = e.Event.ID
			if e.Mismatch == nil && e.Err == nil {
				typed.Trigger = newMessage[O]()
				typed.Err = e.Event.Payload.UnmarshalTo(typed.Trigger)
			}
			select {
			case out <- typed:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// TypedUnregisterTrigger is UnregisterTrigger for method, with input packed.
func TypedUnregisterTrigger[C capabilities.TriggerExecutable, I proto.Message](
	ctx context.Context, cs []C, method, triggerID string, metadata capabilities.RequestMetadata, input I,
) error {
	request, err := triggerRequest(method, triggerID, metadata, input)
	if err != nil {
		return err
	}
	return UnregisterTrigger(ctx, cs, request)
}

func triggerRequest(method, triggerID string, metadata capabilities.RequestMetadata, input proto.Message) (capabilities.TriggerRegistrationRequest, error) {
	payload, err := anypb.New(input)
	return capabilities.TriggerRegistrationRequest{Method: method, TriggerID: triggerID, Metadata: metadata, Payload: payload}, err
}

// newMessage is a new, empty O, which is a pointer to a generated message.
func newMessage[O proto.Message]() O {
	var zero O
	return zero.ProtoReflect().Type().New().Interface().(O)
}

// cloneCapabilityRequest is request with its own copies of what it points to, so an instance that changes
// its request cannot change another's.
func cloneCapabilityRequest(request capabilities.CapabilityRequest) capabilities.CapabilityRequest {
	request.Metadata.SpendLimits = slices.Clone(request.Metadata.SpendLimits)
	request.Payload = cloneAny(request.Payload)
	request.ConfigPayload = cloneAny(request.ConfigPayload)
	return request
}

// cloneTriggerRequest is cloneCapabilityRequest for a trigger registration.
func cloneTriggerRequest(request capabilities.TriggerRegistrationRequest) capabilities.TriggerRegistrationRequest {
	request.Metadata.SpendLimits = slices.Clone(request.Metadata.SpendLimits)
	request.Payload = cloneAny(request.Payload)
	return request
}

func cloneAny(a *anypb.Any) *anypb.Any {
	if a == nil {
		return nil
	}
	return proto.Clone(a).(*anypb.Any)
}

// each calls every instance at once, since a DON's instances may wait on each other.
func each[C, T any](cs []C, call func(C) (T, error)) ([]Result[T], error) {
	if len(cs) == 0 {
		return nil, errors.New("no instances to call")
	}

	results := make([]Result[T], len(cs))
	var wg sync.WaitGroup
	for i, c := range cs {
		wg.Go(func() {
			v, err := call(c)
			results[i] = Result[T]{Value: v, Err: err}
		})
	}
	wg.Wait()
	return results, nil
}

// agree is the result every instance returned, or a *MismatchError when they differ.
func agree[T any](results []Result[T], equal func(a, b T) bool) (T, error) {
	if m := mismatch(results, equal); m != nil {
		var zero T
		return zero, m
	}
	return results[0].Value, results[0].Err
}

// mismatch is nil when every result has the same error and values equal says are the same.
func mismatch[T any](results []Result[T], equal func(a, b T) bool) *MismatchError[T] {
	first := results[0]
	for _, r := range results[1:] {
		if !sameError(first.Err, r.Err) || !equal(first.Value, r.Value) {
			return &MismatchError[T]{Results: results, equal: equal}
		}
	}
	return nil
}

func sameError(a, b error) bool {
	if a == nil || b == nil {
		return a == b //nolint:errorlint // one side is nil, so this is a nil check rather than a sentinel compare
	}
	return a.Error() == b.Error()
}

func describeError(err error) string {
	if err == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%q", err.Error())
}

// samePayload compares the messages two payloads hold rather than their bytes, which encoding need not
// make identical for equal messages, falling back to the bytes for a type this binary cannot decode.
func samePayload(a, b *anypb.Any) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.GetTypeUrl() != b.GetTypeUrl() {
		return false
	}
	am, aErr := a.UnmarshalNew()
	bm, bErr := b.UnmarshalNew()
	if aErr != nil || bErr != nil {
		return proto.Equal(a, b)
	}
	return proto.Equal(am, bm)
}
