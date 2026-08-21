// Package ui serves a debug page for the capabilities a binary hosts: a form per
// method, filled in and invoked from a browser.
//
// It is not a second way into a capability. Every call it makes goes through the
// capability registry, as any caller would, wrapped in the CapabilityRequest a
// host would have sent - so what the page exercises is the same path a workflow
// takes, with the request metadata a workflow would have carried made visible and
// editable instead of implied.
//
// A trigger is offered too, but not as a call: it is registered, and then it
// delivers. So it is offered as a synthetic unary method that registers it (see
// shim.go), and what it delivers is collected per trigger ID into a table with a
// column per instance (see hub.go) and streamed to the browser (see stream.go).
// Instances are meant to agree on an event's payload, so what the table shows is
// whether they did.
package ui

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
)

// call is one capability method the page can invoke.
//
// The input and output are reflect.Types rather than messages: there is no
// instance to hold on to, only the types the descriptor names, and each request
// needs its own anyway.
type call struct {
	// subscribe says this method registers a trigger rather than calling
	// something. What it returns then arrives on a subscription instead of in the
	// response - see shim.go for why a trigger is offered as a unary method at
	// all, and hub.go for where its events go.
	subscribe bool

	// capabilityID and method are what a CapabilityRequest is addressed with.
	// They are held here rather than worked out per request because the
	// descriptor a capability handed over is what named them.
	capabilityID string
	method       string

	// service is the real service this method belongs to. For a subscription that
	// is not the service the request named: the page offers a synthetic one, and
	// what is being subscribed to is the capability's own.
	service string

	input  reflect.Type
	output reflect.Type

	// event is the trigger's streamed message, for a subscription. It is how a
	// delivered event is read out of the Any it arrives in.
	event reflect.Type
}

// Registry is the part of a capability registry this package needs: resolving a
// capability by ID. Anything a host holds satisfies it, and asking for only this
// keeps the page from being able to do more than call and subscribe.
type Registry interface {
	GetExecutable(ctx context.Context, id string) (capabilities.ExecutableCapability, error)
	GetTrigger(ctx context.Context, id string) (capabilities.TriggerCapability, error)
}

// Capability is the part of a hosted capability this package needs: what it is
// registered as, and the proto service it was generated from.
//
// Declared here rather than taking the hosting package's own interface, which
// would be a cycle: the host mounts this, so this cannot import the host.
// Anything a host hands over satisfies it already.
type Capability interface {
	Info(ctx context.Context) (capabilities.CapabilityInfo, error)
	Service() protoreflect.ServiceDescriptor
}

// Server holds the capability methods it can invoke, keyed by the gRPC method the
// browser asks for: "<service full name>/<method>", which is what arrives.
//
// One Server covers every capability the binary hosts, so the page offers all of
// their services together and a request picks one.
type Server struct {
	calls    map[string]call
	registry Registry

	// files and methods are what the form generator is built from: the same
	// descriptors the capabilities were generated against.
	files   []*desc.FileDescriptor
	methods []*desc.MethodDescriptor

	// hub holds the subscriptions this instance has registered. Shared with every
	// other instance, so a subscription registered across several of them is one
	// thing. Set by Mount, which is where an instance's identity is known; nil
	// means triggers cannot be subscribed to, which is what a Server built without
	// being mounted is.
	hub *Hub
	// index and label are which instance this is, carried onto every event so a
	// row can say which node delivered it.
	index int
	label string
	// prefix is where the pages are mounted, so the form can link to the fan-out
	// page - which is where a subscription's events are shown.
	prefix string
}

// subscriptionServices are the services whose methods register a trigger rather
// than calling one, sorted.
//
// The page needs the list because invoking one does something different: it opens
// a subscription instead of returning a response. Taken from the calls rather than
// from a name, so nothing depends on what the synthetic services are called.
func (s *Server) subscriptionServices() []string {
	seen := map[string]bool{}
	for key, c := range s.calls {
		if !c.subscribe {
			continue
		}
		if service, _, found := strings.Cut(key, "/"); found {
			seen[service] = true
		}
	}

	out := make([]string, 0, len(seen))
	for service := range seen {
		out = append(out, service)
	}
	sort.Strings(out)
	return out
}

// New builds a Server over the capabilities given.
//
// Every method of every capability's service is inserted, so no handler is
// registered per method and nothing closes over a capability: a request is served
// by looking its types up and asking the registry for the capability that owns
// it. A streaming method is a trigger, which cannot be called - it is offered as
// a synthetic method that registers it instead, see shim.go.
func New(ctx context.Context, registry Registry, caps ...Capability) (*Server, error) {
	if registry == nil {
		return nil, fmt.Errorf("a capability registry is required")
	}

	s := &Server{calls: map[string]call{}, registry: registry}
	seen := map[string]bool{}

	for i, c := range caps {
		info, err := c.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to read capability %d info: %w", i, err)
		}
		service := c.Service()
		if service == nil {
			return nil, fmt.Errorf("capability %s returned no service descriptor", info.ID)
		}

		if err := s.addService(info.ID, service, seen); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// addService inserts every method of one service: the callable ones as they are,
// and the streaming ones as the subscriptions that register them.
func (s *Server) addService(capabilityID string, service protoreflect.ServiceDescriptor, seenFiles map[string]bool) error {
	if err := s.addFile(service.ParentFile(), seenFiles); err != nil {
		return err
	}

	methods := service.Methods()
	for i := range methods.Len() {
		md := methods.Get(i)
		// A streaming response is a trigger: registered and delivered, not called.
		// It is offered below instead.
		if md.IsStreamingServer() || md.IsStreamingClient() {
			continue
		}

		input, err := goType(md.Input())
		if err != nil {
			return fmt.Errorf("%s: %w", md.FullName(), err)
		}
		output, err := goType(md.Output())
		if err != nil {
			return fmt.Errorf("%s: %w", md.FullName(), err)
		}

		if err = s.addCall(md, call{
			capabilityID: capabilityID,
			service:      string(service.FullName()),
			method:       string(md.Name()),
			input:        input,
			output:       output,
		}); err != nil {
			return err
		}
	}

	return s.addSubscriptions(capabilityID, service, seenFiles)
}

// addSubscriptions inserts the synthetic method that registers each of a
// service's triggers.
//
// The method the page invokes is the synthetic one, so it is that descriptor's
// name a request arrives under - but what is registered is the trigger, so the
// call it resolves to names the capability's own service and method.
func (s *Server) addSubscriptions(capabilityID string, service protoreflect.ServiceDescriptor, seenFiles map[string]bool) error {
	synthetic, err := subscriptionService(service)
	if err != nil {
		return err
	}
	if synthetic == nil {
		return nil
	}

	if err = s.addFile(synthetic.ParentFile(), seenFiles); err != nil {
		return err
	}

	methods := synthetic.Methods()
	for i := range methods.Len() {
		md := methods.Get(i)

		trigger := service.Methods().ByName(md.Name())
		if trigger == nil {
			return fmt.Errorf("%s offers a subscription to %s, which it does not have", service.FullName(), md.Name())
		}

		input, err := goType(md.Input())
		if err != nil {
			return fmt.Errorf("%s: %w", md.FullName(), err)
		}
		output, err := goType(md.Output())
		if err != nil {
			return fmt.Errorf("%s: %w", md.FullName(), err)
		}
		// The message the trigger streams, which is what an event carries.
		event, err := goType(trigger.Output())
		if err != nil {
			return fmt.Errorf("%s: %w", trigger.FullName(), err)
		}

		if err = s.addCall(md, call{
			subscribe:    true,
			capabilityID: capabilityID,
			service:      string(service.FullName()),
			method:       string(md.Name()),
			input:        input,
			output:       output,
			event:        event,
		}); err != nil {
			return err
		}
	}
	return nil
}

// addFile wraps a file for the form generator, once.
func (s *Server) addFile(file protoreflect.FileDescriptor, seenFiles map[string]bool) error {
	if seenFiles[file.Path()] {
		return nil
	}
	seenFiles[file.Path()] = true

	wrapped, err := desc.WrapFile(file)
	if err != nil {
		return fmt.Errorf("failed to wrap %s: %w", file.Path(), err)
	}
	s.files = append(s.files, wrapped)
	return nil
}

// addCall registers one method under the name a request will arrive as, which is
// the descriptor's own - not the capability method it resolves to.
func (s *Server) addCall(md protoreflect.MethodDescriptor, c call) error {
	key := methodKey(string(md.Parent().FullName()), string(md.Name()))
	if existing, taken := s.calls[key]; taken {
		return fmt.Errorf("%s is served by both %s and %s", key, existing.capabilityID, c.capabilityID)
	}
	s.calls[key] = c

	wrapped, err := desc.WrapMethod(md)
	if err != nil {
		return fmt.Errorf("failed to wrap %s: %w", md.FullName(), err)
	}
	s.methods = append(s.methods, wrapped)
	return nil
}

// methodKey is how a call is looked up: the service and method a request names.
func methodKey(service, method string) string {
	return service + "/" + method
}

// goType is the generated Go type for a message descriptor, found through the
// global registry - the same place the generated code registered it.
func goType(md protoreflect.MessageDescriptor) (reflect.Type, error) {
	mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName())
	if err != nil {
		return nil, fmt.Errorf("no Go type registered for %s: %w", md.FullName(), err)
	}
	return reflect.TypeOf(mt.New().Interface()), nil
}

// methodDescriptor is the wrapped descriptor of one registered method, by name.
func (s *Server) methodDescriptor(name string) (*desc.MethodDescriptor, error) {
	for _, m := range s.methods {
		if m.GetName() == name {
			return m, nil
		}
	}
	return nil, fmt.Errorf("no method %q is registered", name)
}

// Methods and Files are the descriptors the form generator renders from.
func (s *Server) Methods() []*desc.MethodDescriptor { return s.methods }
func (s *Server) Files() []*desc.FileDescriptor     { return s.files }

// Invoke is the gRPC client the form generator calls, and where a form becomes a
// capability call: the dynamic request is decoded into the method's own input
// type, wrapped in the CapabilityRequest a host would have sent, and handed to
// the capability the registry resolves.
//
// It satisfies grpc.ClientConnInterface, so the form generator can be pointed at
// this instead of a connection. Nothing is dialled: the registry already knows
// how to reach a capability, whether that is a value in this process or an
// address it holds.
func (s *Server) Invoke(ctx context.Context, method string, args, reply any, _ ...grpc.CallOption) error {
	c, ok := s.calls[strings.TrimPrefix(method, "/")]
	if !ok {
		return userErrorf("no capability method registered for %q", method)
	}
	if c.subscribe {
		return s.subscribe(ctx, c, args, reply, method)
	}

	input, err := s.decode(args, c)
	if err != nil {
		return err
	}

	payload, err := anypb.New(input)
	if err != nil {
		return systemErrorf("failed to wrap the request for %s: %w", method, err)
	}
	// Config is always empty: it was only ever used by DAG workflows.
	config, err := anypb.New(&emptypb.Empty{})
	if err != nil {
		return systemErrorf("failed to build an empty config: %w", err)
	}

	metadata, err := metadataFromContext(ctx)
	if err != nil {
		return err
	}

	executable, err := s.registry.GetExecutable(ctx, c.capabilityID)
	if err != nil {
		return systemErrorf("failed to resolve capability %s: %w", c.capabilityID, err)
	}

	response, err := executable.Execute(ctx, capabilities.CapabilityRequest{
		Metadata:      metadata,
		Method:        c.method,
		CapabilityId:  c.capabilityID,
		Payload:       payload,
		ConfigPayload: config,
	})
	if err != nil {
		// Reported as the call failing rather than the page failing, and as the
		// caller's fault when the capability says it was theirs.
		return fromCapability(err)
	}
	return s.encode(response, reply, c, method)
}

// decode turns whatever the form generator handed over into the method's input.
func (s *Server) decode(args any, c call) (proto.Message, error) {
	input, ok := reflect.New(c.input.Elem()).Interface().(proto.Message)
	if !ok {
		return nil, systemErrorf("%s is not a protobuf message", c.input)
	}

	switch a := args.(type) {
	case *dynamic.Message:
		if err := a.ConvertTo(protoadapt.MessageV1Of(input)); err != nil {
			return nil, userErrorf("the request does not fit %T: %s", input, err)
		}
		return input, nil
	default:
		// A caller that already holds the generated type can pass it through.
		if reflect.TypeOf(args) != c.input {
			return nil, systemErrorf("unexpected request type: %T (want *dynamic.Message or %s)", args, c.input)
		}
		return args.(proto.Message), nil
	}
}

// subscribe registers a trigger rather than calling a method.
//
// The request is built the same way a call's is - the same form, the same
// metadata, the same registry - and the difference is only what comes back:
// registering answers nothing, and what the trigger delivers arrives on the
// subscription this joins. The trigger ID is what identifies that subscription,
// so registering the same trigger ID on another instance joins this one.
func (s *Server) subscribe(ctx context.Context, c call, args, reply any, method string) error {
	if s.hub == nil {
		return systemErrorf("%s cannot be subscribed to: this page was built without a subscription hub", method)
	}

	input, err := s.decode(args, c)
	if err != nil {
		return err
	}

	payload, err := anypb.New(input)
	if err != nil {
		return systemErrorf("failed to wrap the registration for %s: %w", method, err)
	}

	metadata, err := metadataFromContext(ctx)
	if err != nil {
		return err
	}

	trigger, err := s.registry.GetTrigger(ctx, c.capabilityID)
	if err != nil {
		return systemErrorf("failed to resolve trigger capability %s: %w", c.capabilityID, err)
	}

	if _, err = s.hub.subscribe(registration{
		triggerID:    triggerIDFromContext(ctx),
		capabilityID: c.capabilityID,
		service:      c.service,
		method:       c.method,
		instance:     s.index,
		label:        s.label,
		trigger:      trigger,
		metadata:     metadata,
		payload:      payload,
		eventType:    c.event,
	}); err != nil {
		return err
	}

	// Registering is all this call did, and the method says so: its response type
	// is empty. What was registered is now delivering to the subscription.
	return s.fill(reply, &emptypb.Empty{}, c, method)
}

// encode unwraps the capability's response into the reply the form generator
// gave us to fill in.
func (s *Server) encode(response capabilities.CapabilityResponse, reply any, c call, method string) error {
	if response.Payload == nil {
		return systemErrorf("%s returned no payload", method)
	}

	output, ok := reflect.New(c.output.Elem()).Interface().(proto.Message)
	if !ok {
		return systemErrorf("%s is not a protobuf message", c.output)
	}
	if err := response.Payload.UnmarshalTo(output); err != nil {
		return systemErrorf("failed to read the response of %s as %T: %w", method, output, err)
	}
	return s.fill(reply, output, c, method)
}

// fill puts a message into the reply the form generator gave us.
func (s *Server) fill(reply any, output proto.Message, c call, method string) error {
	switch r := reply.(type) {
	case *dynamic.Message:
		if err := r.ConvertFrom(protoadapt.MessageV1Of(output)); err != nil {
			return systemErrorf("failed to encode the response of %s: %w", method, err)
		}
		return nil
	default:
		if reflect.TypeOf(reply) != c.output {
			return systemErrorf("unexpected reply type: %T (want *dynamic.Message or %s)", reply, c.output)
		}
		proto.Reset(reply.(proto.Message))
		proto.Merge(reply.(proto.Message), output)
		return nil
	}
}

// NewStream is here to satisfy grpc.ClientConnInterface. Nothing is opened
// through it: a streaming method is a trigger, and a trigger is registered by the
// synthetic method beside it rather than streamed from here.
func (s *Server) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, userErrorf("streaming is not supported: a trigger is registered through its %s service, and its events arrive on a subscription", SubscriptionsSuffix)
}

var _ grpc.ClientConnInterface = (*Server)(nil)
