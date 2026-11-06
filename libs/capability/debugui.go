package capability

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/capabilities/libs/capability/instances"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// DebugPath is where ServeDebugUI serves the debug UI.
const DebugPath = "/debug/capabilities"

func (m Method) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Name    string
		Trigger bool
		Input   protoreflect.FullName
		Output  protoreflect.FullName
	}{m.Name, m.Trigger, m.Input.ProtoReflect().Descriptor().FullName(), m.Output.ProtoReflect().Descriptor().FullName()})
}

//go:embed debugui.html
var debugPage string

var debugPageTemplate = template.Must(template.New("index").Parse(debugPage))

type debugHandler struct {
	instances []Capability
	methods   map[string]Method
	page      []byte
	debugSubscriptions

	// cancel cancels every trigger registered from the page; running counts the calls and subscriptions shutdown waits
	// for, and closing stops new ones.
	cancel  context.CancelFunc
	running sync.WaitGroup
	closing bool
}

// ServeDebugUI serves a page on mux, at DebugPath, that calls the methods of the instances of a capability, every
// instance at once, showing where they disagree. Triggers registered from the page stay registered until they are
// stopped from it, ctx is done, or the returned shutdown is called. Shutdown stops taking calls and waits for those
// in progress and for the triggers to be unregistered, so call it before closing the capabilities.
func ServeDebugUI[T Capability](ctx context.Context, mux *http.ServeMux, cs []T) (shutdown func(), err error) {
	if len(cs) == 0 {
		return nil, errors.New("no instances to serve")
	}

	info, err := cs[0].Info(ctx)
	if err != nil {
		return nil, err
	}

	all := make([]Capability, len(cs))
	for i, c := range cs {
		all[i] = c
	}

	ctx, cancel := context.WithCancel(ctx)
	h := &debugHandler{instances: all, methods: map[string]Method{}, debugSubscriptions: newDebugSubscriptions(ctx.Done()), cancel: cancel}
	for _, m := range cs[0].Methods() {
		h.methods[m.Name] = m
	}

	if h.page, err = renderPage(info.ID, len(cs), cs[0].Methods()); err != nil {
		cancel()
		return nil, err
	}

	// The page's requests are relative to it, so it is only served with the trailing slash.
	mux.Handle("GET "+DebugPath, http.RedirectHandler(DebugPath+"/", http.StatusMovedPermanently))
	mux.HandleFunc("GET "+DebugPath+"/{$}", h.index)
	mux.HandleFunc("GET "+DebugPath+"/events", h.stream)
	mux.HandleFunc("GET "+DebugPath+"/metadata", h.realisticMetadata)
	protect := http.NewCrossOriginProtection()
	mux.Handle("POST "+DebugPath+"/invoke", protect.Handler(h.untilShutdown(h.invoke)))
	mux.Handle("POST "+DebugPath+"/unsubscribe", protect.Handler(h.untilShutdown(h.unsubscribe)))
	mux.Handle("POST "+DebugPath+"/delete", protect.Handler(h.untilShutdown(h.remove)))
	return h.shutdown, nil
}

func (h *debugHandler) shutdown() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	h.cancel()
	h.running.Wait()
}

func (h *debugHandler) untilShutdown(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.begin() {
			http.Error(w, "the debug UI is shutting down", http.StatusServiceUnavailable)
			return
		}

		defer h.running.Done()
		next(w, r)
	})
}

func (h *debugHandler) begin() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return false
	}

	h.running.Add(1)
	return true
}

func renderPage(title string, instances int, methods []Method) ([]byte, error) {
	types := formSchema{}
	inputs := map[string]formField{}
	for _, m := range methods {
		inputs[m.Name] = types.input(m.Input.ProtoReflect().Descriptor())
	}

	var b bytes.Buffer
	err := debugPageTemplate.Execute(&b, map[string]any{
		"Title":     title,
		"Instances": instances,
		"Page": map[string]any{
			"Methods":   methods,
			"Inputs":    inputs,
			"Instances": instances,
			"MaxEvents": maxDebugEvents,
			"Schema":    types,
			"Metadata":  types.goStruct(reflect.TypeFor[capabilities.RequestMetadata]()),
		},
	})

	return b.Bytes(), err
}

func (h *debugHandler) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(h.page)
}

type debugResult struct {
	TriggerID string
	ID        string
	// Output is left out when there is none, so a null output is still shown.
	Output    json.RawMessage `json:",omitempty"`
	Error     string
	Instances []debugResult
	// Instance is which instance a result is from: a grouped call may skip instances, so position in Instances
	// need not be the instance's index.
	Instance *int `json:",omitempty"`
}

type debugCall struct {
	Method   string
	Metadata capabilities.RequestMetadata
	Input    json.RawMessage
	// Groups, when set, replace Input: each claims a disjoint set of instances and carries the input sent to
	// them, so the instances can be asked different things.
	Groups   []debugGroup
	HexBytes bool
}

type debugGroup struct {
	Instances []int
	Input     json.RawMessage
}

func (h *debugHandler) invoke(w http.ResponseWriter, r *http.Request) {
	var call debugCall
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m, ok := h.methods[call.Method]
	if !ok {
		http.Error(w, "no method "+call.Method, http.StatusBadRequest)
		return
	}

	fillDebugMetadata(&call.Metadata)
	show := func(payload *anypb.Any, err error) debugResult {
		return debugOutput(m.Output, payload, err, call.HexBytes)
	}

	if len(call.Groups) > 0 {
		h.invokeGrouped(w, r, m, call, show)
		return
	}

	payload, err := decodeInput(m, call.Input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if m.Trigger {
		id := "debug-" + randomDebugHex(6)
		requests := repeatRequest(h.instances, capabilities.TriggerRegistrationRequest{
			TriggerID: id, Method: m.Name, Metadata: call.Metadata, Payload: payload,
		})
		if err = h.subscribe(h.instances, nil, requests, call.Input, show); err != nil {
			writeDebugJSON(w, debugResult{Error: err.Error()})
			return
		}

		writeDebugJSON(w, debugResult{TriggerID: id})
		return
	}

	writeDebugJSON(w, h.execute(r.Context(), show, h.instances, nil, repeatRequest(h.instances, capabilities.CapabilityRequest{
		Method: m.Name, Metadata: call.Metadata, Payload: payload,
	})))
}

// invokeGrouped answers a call whose groups send each set of instances its own input. Instances in no group are
// not called: leaving some out is the point of grouping.
func (h *debugHandler) invokeGrouped(w http.ResponseWriter, r *http.Request, m Method, call debugCall, show debugDisplay) {
	payloads, err := h.groupPayloads(m, call.Groups)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// called is the instances in a group, with their payloads; the rest are skipped.
	var called []Capability
	var indexes []int
	for i, payload := range payloads {
		if payload == nil {
			continue
		}
		called = append(called, h.instances[i])
		indexes = append(indexes, i)
	}

	if m.Trigger {
		id := "debug-" + randomDebugHex(6)
		requests := make([]capabilities.TriggerRegistrationRequest, len(called))
		for j, i := range indexes {
			requests[j] = capabilities.TriggerRegistrationRequest{TriggerID: id, Method: m.Name, Metadata: call.Metadata, Payload: payloads[i]}
		}

		// The groups are the input shown for the subscription: there is no one input to show.
		input, _ := json.Marshal(call.Groups)
		if err = h.subscribe(called, indexes, requests, input, show); err != nil {
			writeDebugJSON(w, debugResult{Error: err.Error()})
			return
		}

		writeDebugJSON(w, debugResult{TriggerID: id})
		return
	}

	requests := make([]capabilities.CapabilityRequest, len(called))
	for j, i := range indexes {
		requests[j] = capabilities.CapabilityRequest{Method: m.Name, Metadata: call.Metadata, Payload: payloads[i]}
	}

	// The inputs differ per group; the answer must not. Consensus is every instance returning the same thing
	// regardless of input, so the results are compared across every called instance, not within groups.
	writeDebugJSON(w, h.execute(r.Context(), show, called, indexes, requests))
}

func (h *debugHandler) groupPayloads(m Method, groups []debugGroup) ([]*anypb.Any, error) {
	payloads := make([]*anypb.Any, len(h.instances))
	called := 0
	for gi, g := range groups {
		if len(g.Instances) == 0 {
			return nil, fmt.Errorf("group %d claims no instances", gi+1)
		}

		payload, err := decodeInput(m, g.Input)
		if err != nil {
			return nil, fmt.Errorf("group %d: %w", gi+1, err)
		}

		for _, i := range g.Instances {
			if i < 0 || i >= len(h.instances) {
				return nil, fmt.Errorf("group %d: no instance %d", gi+1, i)
			}
			if payloads[i] != nil {
				return nil, fmt.Errorf("instance %d is in two groups", i)
			}
			payloads[i] = payload
			called++
		}
	}

	if called == 0 {
		return nil, errors.New("no instance is in any group")
	}

	return payloads, nil
}

func repeatRequest[T any](cs []Capability, request T) []T {
	requests := make([]T, len(cs))
	for i := range requests {
		requests[i] = request
	}
	return requests
}

func decodeInput(m Method, raw json.RawMessage) (*anypb.Any, error) {
	input := m.Input.ProtoReflect().New().Interface()
	if err := fromPageJSON(raw, input); err != nil {
		return nil, err
	}

	return anypb.New(input)
}

// realisticMetadata answers the page's "populate realistic values" with metadata filled the same way invoke
// fills blanks, so the page can show what a real request carries and keep it across invocations.
func (h *debugHandler) realisticMetadata(w http.ResponseWriter, _ *http.Request) {
	var md capabilities.RequestMetadata
	fillDebugMetadata(&md)
	writeDebugJSON(w, md)
}

func fillDebugMetadata(md *capabilities.RequestMetadata) {
	fill := func(field *string, value string) {
		if *field == "" {
			*field = value
		}
	}

	// IDs are fresh per request, with the hex lengths a consensus report carries.
	fill(&md.WorkflowID, randomDebugHex(32))
	fill(&md.WorkflowOwner, randomDebugHex(20))
	fill(&md.WorkflowName, randomDebugHex(10))
	fill(&md.WorkflowExecutionID, randomDebugHex(32))
	fill(&md.DecodedWorkflowName, "debug")
	fill(&md.ReferenceID, "debug")
	fill(&md.EngineVersion, "v1")
	if md.WorkflowDonID == 0 {
		md.WorkflowDonID = 1
	}

	if md.WorkflowDonConfigVersion == 0 {
		md.WorkflowDonConfigVersion = 1
	}

	if md.ExecutionTimestamp.IsZero() {
		md.ExecutionTimestamp = time.Now().UTC()
	}
}

type debugDisplay func(payload *anypb.Any, err error) debugResult

// execute runs the requests, one per instance of cs, and shows the response they agree on. indexes, when set,
// says which instances cs is, so a grouped call's per-instance results are labeled with them.
func (h *debugHandler) execute(ctx context.Context, show debugDisplay, cs []Capability, indexes []int, requests []capabilities.CapabilityRequest) debugResult {
	response, err := instances.ExecuteEach(ctx, cs, requests)
	if mismatch, ok := errors.AsType[*instances.MismatchError[capabilities.CapabilityResponse]](err); ok {
		out := debugPerInstance(mismatch, func(r capabilities.CapabilityResponse, err error) debugResult {
			return show(r.Payload, err)
		})
		for j := range out.Instances {
			if indexes != nil {
				out.Instances[j].Instance = &indexes[j]
			}
		}
		return out
	}

	return show(response.Payload, err)
}

// subscribe registers the requests on cs, one per instance; cs may be a subset of every instance, as a grouped
// call's is, with indexes saying which instances they are.
func (h *debugHandler) subscribe(cs []Capability, indexes []int, requests []capabilities.TriggerRegistrationRequest, input json.RawMessage, show debugDisplay) error {
	ctx, stop := context.WithCancel(context.Background())
	// Before registering, so shutting down also cancels a registration in progress.
	go func() {
		select {
		case <-h.done:
			stop()
		case <-ctx.Done():
		}
	}()

	events, err := instances.RegisterTriggerEach(ctx, cs, requests)
	if err != nil {
		stop()
		return err
	}

	// The trigger ID and method are the same on every request, grouped or not.
	id, method := requests[0].TriggerID, requests[0].Method
	sub := &debugSubscription{
		id: id, method: method, input: input, stop: stop, done: make(chan struct{}),
		unregister: unregisterOutstanding(cs, requests),
	}
	h.add(sub)
	// The invoke registering it is still counted, so this can't race shutdown's Wait.
	h.running.Go(func() {
		// Stopping releases the shutdown watcher once the trigger ends by itself too.
		defer stop()
		defer close(sub.done)
		for e := range events {
			h.publish(id, h.event(ctx, cs, indexes, id, method, show, e))
		}

		h.stopped(sub, sub.unregister(context.WithoutCancel(ctx)))
	})

	return nil
}

// unregisterOutstanding unregisters the trigger from every instance it's still registered on, so a retry after some
// failed doesn't unregister it again from those that succeeded.
func unregisterOutstanding(cs []Capability, requests []capabilities.TriggerRegistrationRequest) func(context.Context) error {
	outstanding := repeatRequest(cs, true)
	return func(ctx context.Context) error {
		var errs []error
		for i, c := range cs {
			if !outstanding[i] {
				continue
			}
			// One at a time rather than UnregisterTrigger on the set: the requests may differ per instance.
			if err := c.UnregisterTrigger(ctx, requests[i]); err != nil {
				errs = append(errs, err)
				continue
			}
			outstanding[i] = false
		}
		return errors.Join(errs...)
	}
}

// event shows e, and acks it to every instance that delivered it, whether or not it could be shown or the instances
// agreed, as they won't deliver it again. Positions in e index cs; indexes, when set, says which instances those
// are, so a grouped event's results are labeled with them.
func (h *debugHandler) event(ctx context.Context, cs []Capability, indexes []int, triggerID, method string, show debugDisplay, e instances.Event) debugResult {
	delivered := make([]string, len(cs))
	var out debugResult
	if e.Mismatch != nil {
		out = debugPerInstance(e.Mismatch, func(r capabilities.TriggerResponse, err error) debugResult {
			return debugEvent(show, r, err)
		})

		// Whatever the error, an event with an ID was delivered, and won't be again.
		for i, r := range e.Mismatch.Results {
			delivered[i] = r.Value.Event.ID
		}
	} else {
		out = debugEvent(show, e.TriggerResponse, e.Err)
		for i := range delivered {
			delivered[i] = e.Event.ID
		}
	}

	// A grouped event's instances are a subset; label each result with the instance it is from.
	for j := range out.Instances {
		if indexes != nil {
			out.Instances[j].Instance = &indexes[j]
		}
	}

	var errs []error
	for i, id := range delivered {
		if id != "" {
			errs = append(errs, cs[i].AckEvent(ctx, triggerID, id, method))
		}
	}

	if err := errors.Join(errs...); err != nil {
		out.Error = strings.TrimSpace(out.Error + "\nfailed to ack: " + err.Error())
	}

	return out
}

func debugEvent(show debugDisplay, r capabilities.TriggerResponse, err error) debugResult {
	out := show(r.Event.Payload, err)
	out.ID = r.Event.ID
	return out
}

func debugPerInstance[T any](m *instances.MismatchError[T], show func(T, error) debugResult) debugResult {
	r := debugResult{Error: m.Error()}
	for _, each := range m.Results {
		r.Instances = append(r.Instances, show(each.Value, each.Err))
	}

	return r
}

func debugOutput(out proto.Message, payload *anypb.Any, err error, hexBytes bool) debugResult {
	if err != nil {
		return debugResult{Error: err.Error()}
	}

	msg := out.ProtoReflect().New().Interface()
	if err = payload.UnmarshalTo(msg); err != nil {
		return debugResult{Error: err.Error()}
	}

	b, err := toPageJSON(msg, hexBytes)
	if err != nil {
		return debugResult{Error: err.Error()}
	}

	return debugResult{Output: b}
}

func writeDebugJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func randomDebugHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
