// Package ui serves a debug page for the capabilities a binary hosts: a form per
// method, filled in and invoked from a browser.
//
// It is not a second way into a capability. Every call it makes goes through the
// capability registry, as any caller would, wrapped in the CapabilityRequest a
// host would have sent - so what the page exercises is the same path a workflow
// takes, with the request metadata a workflow would have carried made visible and
// editable instead of implied.
package ui

import (
	"context"
	"fmt"
	"reflect"
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
	// capabilityID and method are what a CapabilityRequest is addressed with.
	// They are held here rather than worked out per request because the
	// descriptor a capability handed over is what named them.
	capabilityID string
	method       string

	input  reflect.Type
	output reflect.Type
}

// Registry is the part of a capability registry this package needs: resolving an
// executable by ID. Anything a host holds satisfies it, and asking for only this
// keeps the page from being able to do more than call.
type Registry interface {
	GetExecutable(ctx context.Context, id string) (capabilities.ExecutableCapability, error)
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
}

// New builds a Server over the capabilities given.
//
// Every non-streaming method of every capability's service is inserted, so no
// handler is registered per method and nothing closes over a capability: a
// request is served by looking its types up and asking the registry for the
// capability that owns it. Streaming methods are triggers, which are registered
// and delivered rather than called, so they are left out.
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

// addService inserts every callable method of one service.
func (s *Server) addService(capabilityID string, service protoreflect.ServiceDescriptor, seenFiles map[string]bool) error {
	file := service.ParentFile()
	if !seenFiles[file.Path()] {
		seenFiles[file.Path()] = true
		wrapped, err := desc.WrapFile(file)
		if err != nil {
			return fmt.Errorf("failed to wrap %s: %w", file.Path(), err)
		}
		s.files = append(s.files, wrapped)
	}

	methods := service.Methods()
	for i := range methods.Len() {
		md := methods.Get(i)
		// A streaming response is a trigger: registered and delivered, not called.
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

		key := methodKey(string(service.FullName()), string(md.Name()))
		if existing, taken := s.calls[key]; taken {
			return fmt.Errorf("%s is served by both %s and %s", key, existing.capabilityID, capabilityID)
		}
		s.calls[key] = call{
			capabilityID: capabilityID,
			method:       string(md.Name()),
			input:        input,
			output:       output,
		}

		wrapped, err := desc.WrapMethod(md)
		if err != nil {
			return fmt.Errorf("failed to wrap %s: %w", md.FullName(), err)
		}
		s.methods = append(s.methods, wrapped)
	}
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

// NewStream is here to satisfy grpc.ClientConnInterface. Streaming methods are
// triggers, which this page does not offer, so there is nothing to open.
func (s *Server) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, userErrorf("streaming is not supported: triggers are registered rather than called")
}

var _ grpc.ClientConnInterface = (*Server)(nil)
