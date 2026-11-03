package capability

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
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
}

type debugCall struct {
	Method   string
	Metadata capabilities.RequestMetadata
	Input    json.RawMessage
	HexBytes bool
}

func (h *debugHandler) invoke(w http.ResponseWriter, r *http.Request) {
	var call debugCall
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m, payload, err := h.decode(call)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	fillDebugMetadata(&call.Metadata)
	show := func(payload *anypb.Any, err error) debugResult {
		return debugOutput(m.Output, payload, err, call.HexBytes)
	}

	if m.Trigger {
		writeDebugJSON(w, h.register(m, call, payload, show))
		return
	}

	writeDebugJSON(w, h.execute(r.Context(), show, capabilities.CapabilityRequest{
		Method: m.Name, Metadata: call.Metadata, Payload: payload,
	}))
}

func (h *debugHandler) decode(call debugCall) (Method, *anypb.Any, error) {
	m, ok := h.methods[call.Method]
	if !ok {
		return Method{}, nil, errors.New("no method " + call.Method)
	}

	input := m.Input.ProtoReflect().New().Interface()
	if err := fromPageJSON(call.Input, input); err != nil {
		return Method{}, nil, err
	}

	payload, err := anypb.New(input)
	return m, payload, err
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

func (h *debugHandler) register(m Method, call debugCall, payload *anypb.Any, show debugDisplay) debugResult {
	request := capabilities.TriggerRegistrationRequest{
		TriggerID: "debug-" + randomDebugHex(6), Method: m.Name, Metadata: call.Metadata, Payload: payload,
	}

	if err := h.subscribe(request, call.Input, show); err != nil {
		return debugResult{Error: err.Error()}
	}

	return debugResult{TriggerID: request.TriggerID}
}

type debugDisplay func(payload *anypb.Any, err error) debugResult

func (h *debugHandler) execute(ctx context.Context, show debugDisplay, request capabilities.CapabilityRequest) debugResult {
	response, err := instances.Execute(ctx, h.instances, request)
	if mismatch, ok := errors.AsType[*instances.MismatchError[capabilities.CapabilityResponse]](err); ok {
		return debugPerInstance(mismatch, func(r capabilities.CapabilityResponse, err error) debugResult {
			return show(r.Payload, err)
		})
	}

	return show(response.Payload, err)
}

func (h *debugHandler) subscribe(request capabilities.TriggerRegistrationRequest, input json.RawMessage, show debugDisplay) error {
	ctx, stop := context.WithCancel(context.Background())
	// Before registering, so shutting down also cancels a registration in progress.
	go func() {
		select {
		case <-h.done:
			stop()
		case <-ctx.Done():
		}
	}()

	events, err := instances.RegisterTrigger(ctx, h.instances, request)
	if err != nil {
		stop()
		return err
	}

	sub := &debugSubscription{
		id: request.TriggerID, method: request.Method, input: input, stop: stop, done: make(chan struct{}),
		unregister: unregisterOutstanding(h.instances, request),
	}
	h.add(sub)
	// The invoke registering it is still counted, so this can't race shutdown's Wait.
	h.running.Add(1)
	go func() {
		defer h.running.Done()
		// Stopping releases the shutdown watcher once the trigger ends by itself too.
		defer stop()
		defer close(sub.done)
		for e := range events {
			h.publish(request.TriggerID, h.event(ctx, request, show, e))
		}

		h.stopped(sub, sub.unregister(context.WithoutCancel(ctx)))
	}()

	return nil
}

// unregisterOutstanding unregisters the trigger from every instance it's still registered on, so a retry after some
// failed doesn't unregister it again from those that succeeded.
func unregisterOutstanding(cs []Capability, request capabilities.TriggerRegistrationRequest) func(context.Context) error {
	outstanding := cs
	return func(ctx context.Context) error {
		err := instances.UnregisterTrigger(ctx, outstanding, request)
		if mismatch, ok := errors.AsType[*instances.MismatchError[struct{}]](err); ok {
			var left []Capability
			for i, r := range mismatch.Results {
				if r.Err != nil {
					left = append(left, outstanding[i])
				}
			}

			outstanding = left
		}

		return err
	}
}

// event shows e, and acks it to every instance that delivered it, whether or not it could be shown or the instances
// agreed, as they won't deliver it again.
func (h *debugHandler) event(ctx context.Context, request capabilities.TriggerRegistrationRequest, show debugDisplay, e instances.Event) debugResult {
	delivered := make([]string, len(h.instances))
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

	var errs []error
	for i, id := range delivered {
		if id != "" {
			errs = append(errs, h.instances[i].AckEvent(ctx, request.TriggerID, id, request.Method))
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
