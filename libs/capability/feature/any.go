package feature

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
)

// Development is the segment that sets a capability's development proto package apart from its production one,
// which is the development package without it: capabilities.scheduler.cron.x.v1 is developed, and released as
// capabilities.scheduler.cron.v1. The production package is what the SDKs use, so it never changes.
const Development = "x"

func ProductionPackage(pkg protoreflect.FullName) (protoreflect.FullName, error) {
	parts := strings.Split(string(pkg), ".")
	n := len(parts)
	if n < 3 || parts[n-2] != Development {
		return "", fmt.Errorf("%s is not a development package: it must end in .%s.<version>", pkg, Development)
	}

	return protoreflect.FullName(strings.Join(slices.Delete(parts, n-2, n-1), ".")), nil
}

func ProductionName(d protoreflect.Descriptor) (protoreflect.FullName, error) {
	pkg := d.ParentFile().Package()
	prod, err := ProductionPackage(pkg)
	if err != nil {
		return "", err
	}

	return prod + d.FullName()[len(pkg):], nil
}

// UnmarshalAny unmarshals a into the development message m, whether a holds m or its production counterpart,
// reporting whether it was the production one.
func UnmarshalAny(a *anypb.Any, m proto.Message) (bool, error) {
	name := m.ProtoReflect().Descriptor().FullName()
	switch a.MessageName() {
	case name:
		return false, a.UnmarshalTo(m)
	default:
		prod, err := ProductionName(m.ProtoReflect().Descriptor())
		if err != nil {
			return false, err
		}
		if a.MessageName() != prod {
			return false, fmt.Errorf("mismatched message type: got %q, want %q or %q", a.MessageName(), name, prod)
		}
		return true, proto.Unmarshal(a.GetValue(), m)
	}
}

// MarshalAny marshals the development message m into an Any, as its production counterpart if production, so a
// reply is the kind of message its request was, as UnmarshalAny reported.
func MarshalAny(m proto.Message, production bool) (*anypb.Any, error) {
	a, err := anypb.New(m)
	if err != nil || !production {
		return a, err
	}
	prod, err := ProductionName(m.ProtoReflect().Descriptor())
	if err != nil {
		return nil, err
	}
	a.TypeUrl = strings.TrimSuffix(a.TypeUrl, string(a.MessageName())) + string(prod)
	return a, nil
}

// ProductionPath is the path the production version of the development proto file is registered by: in the
// directory of its production package, as the SDKs lay their protos out. It can't be the development file's path,
// as a binary with both, such as one running a workflow beside the capability, would register it twice.
func ProductionPath(pkg protoreflect.FullName, file string) (string, error) {
	prod, err := ProductionPackage(pkg)
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(prod), ".", "/") + "/" + path.Base(file), nil
}
