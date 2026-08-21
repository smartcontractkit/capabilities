package ui

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/emptypb"
)

// A trigger is a stream, and the form the page is built from cannot invoke one:
// grpcui posts a request and renders a response, which is the shape of a unary
// call and nothing else.
//
// So a trigger is offered as a unary method that registers it. The method is
// synthesised here rather than written in a .proto, because what makes it useful
// is the half that cannot be written by hand: its input is the trigger's own
// input message, so the page draws the trigger's real configuration form - a cron
// schedule, a contract address - rather than a box to paste JSON into. Only the
// registering is new, and registering returns nothing worth a message of its own.
//
// The events then arrive over a stream of the page's own (see hub.go), keyed by
// the trigger ID the request carried, so the handle does not have to come back in
// the response either.
const (
	// SubscriptionsSuffix is appended to a service's name to name the synthetic
	// one. A separate service rather than extra methods on the real one: the real
	// descriptor is generated code, and the page listing "Cron" beside
	// "CronSubscriptions" says which of the two a method belongs to.
	SubscriptionsSuffix = "Subscriptions"

	// emptyPath is the file the synthetic methods return a message from.
	emptyPath = "google/protobuf/empty.proto"
)

// subscriptionService is the synthetic service for the streaming methods of
// service, or nil if it has none.
//
// The descriptor is built against the global registry, so the input types it
// names are the same descriptors - and therefore the same generated Go types -
// that the capability's own service names. Nothing is copied.
func subscriptionService(service protoreflect.ServiceDescriptor) (protoreflect.ServiceDescriptor, error) {
	triggers := streamingMethods(service)
	if len(triggers) == 0 {
		return nil, nil
	}

	name := string(service.Name()) + SubscriptionsSuffix
	file := &descriptorpb.FileDescriptorProto{
		// Named after the service it shadows so two capabilities cannot collide,
		// and under a path no .proto could occupy so it cannot shadow a real file.
		Name:   proto.String(fmt.Sprintf("cre/ui/%s.subscriptions.proto", service.FullName())),
		Syntax: proto.String("proto3"),
		// The same proto package, so the synthetic service sits beside the real one
		// rather than in a namespace of its own.
		Dependency: dependencies(service, triggers),
		Service: []*descriptorpb.ServiceDescriptorProto{{
			Name:   proto.String(name),
			Method: methods(triggers),
		}},
	}
	if pkg := service.ParentFile().Package(); pkg != "" {
		file.Package = proto.String(string(pkg))
	}

	fd, err := protodesc.NewFile(file, protoregistry.GlobalFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to build the subscription service for %s: %w", service.FullName(), err)
	}

	synthetic := fd.Services().ByName(protoreflect.Name(name))
	if synthetic == nil {
		return nil, fmt.Errorf("the subscription service for %s built without %s", service.FullName(), name)
	}
	return synthetic, nil
}

// streamingMethods are the methods of service that stream, which are its triggers.
func streamingMethods(service protoreflect.ServiceDescriptor) []protoreflect.MethodDescriptor {
	var out []protoreflect.MethodDescriptor
	all := service.Methods()
	for i := range all.Len() {
		if md := all.Get(i); md.IsStreamingServer() || md.IsStreamingClient() {
			out = append(out, md)
		}
	}
	return out
}

// methods is one unary method per trigger: the trigger's input, and nothing back.
func methods(triggers []protoreflect.MethodDescriptor) []*descriptorpb.MethodDescriptorProto {
	out := make([]*descriptorpb.MethodDescriptorProto, 0, len(triggers))
	for _, md := range triggers {
		out = append(out, &descriptorpb.MethodDescriptorProto{
			// The trigger's own name, so what the page offers is recognisably the
			// method the proto declares rather than a name invented here.
			Name:      proto.String(string(md.Name())),
			InputType: proto.String("." + string(md.Input().FullName())),
			// Registering succeeds or it does not. What the subscription then
			// delivers does not come back through this call.
			OutputType: proto.String("." + string((&emptypb.Empty{}).ProtoReflect().Descriptor().FullName())),
		})
	}
	return out
}

// dependencies are the files the synthetic one has to import: whatever declares
// the input messages, plus the empty it returns.
//
// An input is not always declared in the service's own file - a trigger taking a
// shared type imports it - so each input's file is named rather than assuming the
// service's.
func dependencies(service protoreflect.ServiceDescriptor, triggers []protoreflect.MethodDescriptor) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path)
	}

	add(service.ParentFile().Path())
	for _, md := range triggers {
		add(md.Input().ParentFile().Path())
	}
	add(emptyPath)
	return out
}
