package feature_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"

	"github.com/smartcontractkit/capabilities/libs/capability/feature"
)

const (
	devPackage  = "capabilities.test.client.x.v1"
	prodPackage = "capabilities.test.client.v1"
)

// request returns a new Request from pkg, with message set, and extra too if it has it: production's Request is
// development's without extra.
func request(t *testing.T, pkg string, withExtra bool) *dynamicpb.Message {
	t.Helper()

	fields := []*descriptorpb.FieldDescriptorProto{
		{Name: new("message"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), JsonName: new("message")},
	}
	if withExtra {
		fields = append(fields, &descriptorpb.FieldDescriptorProto{
			Name: new("extra"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(), JsonName: new("extra"),
		})
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    new(pkg + ".proto"),
		Package: new(pkg),
		Syntax:  new("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name:       new("Request"),
			Field:      fields,
			NestedType: []*descriptorpb.DescriptorProto{{Name: new("Nested")}},
		}},
	}, nil)
	require.NoError(t, err)

	m := dynamicpb.NewMessage(fd.Messages().ByName("Request"))
	set(m, "message", "hello")
	if withExtra {
		set(m, "extra", "more")
	}
	return m
}

func set(m *dynamicpb.Message, field, value string) {
	m.Set(m.Descriptor().Fields().ByName(protoreflect.Name(field)), protoreflect.ValueOfString(value))
}

func get(m *dynamicpb.Message, field string) string {
	return m.Get(m.Descriptor().Fields().ByName(protoreflect.Name(field))).String()
}

func TestProductionPackage(t *testing.T) {
	prod, err := feature.ProductionPackage(devPackage)
	require.NoError(t, err)
	assert.Equal(t, protoreflect.FullName(prodPackage), prod)

	prod, err = feature.ProductionPackage("capabilities.blockchain.evm.x.v1alpha")
	require.NoError(t, err)
	assert.Equal(t, protoreflect.FullName("capabilities.blockchain.evm.v1alpha"), prod)

	for _, pkg := range []protoreflect.FullName{prodPackage, "x.v1", "capabilities.test.x", "capabilities.x.test.v1"} {
		_, err = feature.ProductionPackage(pkg)
		assert.Error(t, err, pkg)
	}
}

func TestProductionName(t *testing.T) {
	m := request(t, devPackage, true)

	name, err := feature.ProductionName(m.Descriptor())
	require.NoError(t, err)
	assert.Equal(t, protoreflect.FullName(prodPackage+".Request"), name)

	name, err = feature.ProductionName(m.Descriptor().Messages().ByName("Nested"))
	require.NoError(t, err)
	assert.Equal(t, protoreflect.FullName(prodPackage+".Request.Nested"), name)
}

func TestUnmarshalAnyDevelopment(t *testing.T) {
	a, err := anypb.New(request(t, devPackage, true))
	require.NoError(t, err)

	got := request(t, devPackage, true)
	got.Clear(got.Descriptor().Fields().ByName("message"))
	production, err := feature.UnmarshalAny(a, got)
	require.NoError(t, err)
	assert.False(t, production)
	assert.Equal(t, "hello", get(got, "message"))
	assert.Equal(t, "more", get(got, "extra"))
}

func TestUnmarshalAnyProduction(t *testing.T) {
	a, err := anypb.New(request(t, prodPackage, false))
	require.NoError(t, err)

	got := dynamicpb.NewMessage(request(t, devPackage, true).Descriptor())
	production, err := feature.UnmarshalAny(a, got)
	require.NoError(t, err)
	assert.True(t, production)
	assert.Equal(t, "hello", get(got, "message"))
	assert.Empty(t, get(got, "extra"))
}

func TestUnmarshalAnyMismatch(t *testing.T) {
	a, err := anypb.New(request(t, "capabilities.other.client.v1", false))
	require.NoError(t, err)

	_, err = feature.UnmarshalAny(a, request(t, devPackage, true))
	assert.ErrorContains(t, err, "mismatched message type")

	// A message that is not from a development package has no production counterpart to accept.
	_, err = feature.UnmarshalAny(a, request(t, prodPackage, false))
	assert.ErrorContains(t, err, "not a development package")
}

func TestMarshalAny(t *testing.T) {
	m := request(t, devPackage, true)

	a, err := feature.MarshalAny(m, false)
	require.NoError(t, err)
	assert.Equal(t, "type.googleapis.com/"+devPackage+".Request", a.GetTypeUrl())
	got := dynamicpb.NewMessage(m.Descriptor())
	require.NoError(t, a.UnmarshalTo(got))
	assert.True(t, proto.Equal(m, got))

	a, err = feature.MarshalAny(m, true)
	require.NoError(t, err)
	assert.Equal(t, "type.googleapis.com/"+prodPackage+".Request", a.GetTypeUrl())
	prod := dynamicpb.NewMessage(request(t, prodPackage, false).Descriptor())
	require.NoError(t, a.UnmarshalTo(prod))
	assert.Equal(t, "hello", get(prod, "message"))
}

// TestRoundTrip is a production request answered by a development capability: what UnmarshalAny reports is what
// MarshalAny takes, so the reply is production too.
func TestRoundTrip(t *testing.T) {
	in, err := anypb.New(request(t, prodPackage, false))
	require.NoError(t, err)

	m := dynamicpb.NewMessage(request(t, devPackage, true).Descriptor())
	production, err := feature.UnmarshalAny(in, m)
	require.NoError(t, err)
	out, err := feature.MarshalAny(m, production)
	require.NoError(t, err)
	assert.Equal(t, in.GetTypeUrl(), out.GetTypeUrl())

	again := dynamicpb.NewMessage(m.Descriptor())
	production, err = feature.UnmarshalAny(out, again)
	require.NoError(t, err)
	assert.True(t, production)
	assert.True(t, proto.Equal(m, again))
}

func TestMarshalAnyNotDevelopment(t *testing.T) {
	_, err := feature.MarshalAny(request(t, prodPackage, false), true)
	assert.Error(t, err)

	// Without production, any message marshals as it is.
	a, err := feature.MarshalAny(request(t, prodPackage, false), false)
	require.NoError(t, err)
	assert.Equal(t, protoreflect.FullName(prodPackage+".Request"), a.MessageName())
}

func TestProductionPath(t *testing.T) {
	p, err := feature.ProductionPath(devPackage, "trigger.proto")
	require.NoError(t, err)
	assert.Equal(t, "capabilities/test/client/v1/trigger.proto", p)

	_, err = feature.ProductionPath(prodPackage, "trigger.proto")
	assert.Error(t, err)
}
