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
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/capabilities/libs/capability/instances"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// DebugPath is where ServeDebugUI serves the debug UI.
const DebugPath = "/debug/capabilities"

// Method is a method a capability serves, and the messages it takes and returns, for the debug UI to call it with.
type Method struct {
	Name    string
	Trigger bool
	Input   proto.Message
	Output  proto.Message
}

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
}

// ServeDebugUI serves a page on mux, at DebugPath, that calls the methods of the instances of a capability, every
// instance at once, showing where they disagree. Triggers registered from the page stay registered until they are
// stopped from it or ctx is done.
func ServeDebugUI[T Capability](ctx context.Context, mux *http.ServeMux, cs []T) error {
	if len(cs) == 0 {
		return errors.New("no instances to serve")
	}

	info, err := cs[0].Info(ctx)
	if err != nil {
		return err
	}

	all := make([]Capability, len(cs))
	for i, c := range cs {
		all[i] = c
	}
	h := &debugHandler{instances: all, methods: map[string]Method{}, debugSubscriptions: newDebugSubscriptions(ctx.Done())}
	types := formSchema{}
	methods := cs[0].Methods()
	for _, m := range methods {
		h.methods[m.Name] = m
		types.message(m.Input.ProtoReflect().Descriptor())
	}

	var b bytes.Buffer
	err = debugPageTemplate.Execute(&b, map[string]any{
		"Title":     info.ID,
		"Instances": len(cs),
		"Page": map[string]any{
			"Methods":  methods,
			"Schema":   types,
			"Metadata": types.goStruct(reflect.TypeFor[capabilities.RequestMetadata]()),
		},
	})
	if err != nil {
		return err
	}

	h.page = b.Bytes()

	// The page's requests are relative to it, so it is only served with the trailing slash.
	mux.Handle("GET "+DebugPath, http.RedirectHandler(DebugPath+"/", http.StatusMovedPermanently))
	mux.HandleFunc("GET "+DebugPath+"/{$}", h.index)
	mux.HandleFunc("GET "+DebugPath+"/events", h.stream)
	mux.HandleFunc("POST "+DebugPath+"/invoke", h.invoke)
	mux.HandleFunc("POST "+DebugPath+"/unsubscribe", h.unsubscribe)
	mux.HandleFunc("POST "+DebugPath+"/delete", h.remove)
	return nil
}

func (h *debugHandler) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(h.page)
}

type debugResult struct {
	TriggerID string
	ID        string
	Output    json.RawMessage
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
	events, err := instances.RegisterTrigger(ctx, h.instances, request)
	if err != nil {
		stop()
		return err
	}

	go func() {
		select {
		case <-h.done:
			stop()
		case <-ctx.Done():
		}
	}()

	done := make(chan struct{})
	h.add(&debugSubscription{id: request.TriggerID, method: request.Method, input: input, stop: stop, done: done})
	go func() {
		defer close(done)
		for e := range events {
			if e.Mismatch != nil {
				h.publish(request.TriggerID, debugPerInstance(e.Mismatch, func(r capabilities.TriggerResponse, err error) debugResult {
					return debugEvent(show, r, err)
				}))
				continue
			}

			event := debugEvent(show, e.TriggerResponse, e.Err)
			if event.Error == "" {
				event.Error = h.ack(ctx, request.TriggerID, e.Event.ID, request.Method)
			}

			h.publish(request.TriggerID, event)
		}

		_ = instances.UnregisterTrigger(context.WithoutCancel(ctx), h.instances, request)
		h.close(request.TriggerID)
	}()
	return nil
}

func (h *debugHandler) ack(ctx context.Context, triggerID, eventID, method string) string {
	var errs []error
	for _, c := range h.instances {
		errs = append(errs, c.AckEvent(ctx, triggerID, eventID, method))
	}

	if err := errors.Join(errs...); err != nil {
		return "failed to ack: " + err.Error()
	}

	return ""
}

func debugEvent(show debugDisplay, r capabilities.TriggerResponse, err error) debugResult {
	out := show(r.Event.Payload, err)
	out.ID = r.Event.ID
	return out
}

func debugPerInstance[T any](m *instances.MismatchError[T], show func(T, error) debugResult) debugResult {
	r := debugResult{Error: "instances disagree"}
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
