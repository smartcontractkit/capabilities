package capability

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/emptypb"
	_ "google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

func TestJSON(t *testing.T) {
	for _, tt := range []struct{ name, page string }{
		{"bytes", `{"bytes_value": "0x00ff"}`},
		{"bigint", `{"bigint_value": "-123456789012345678901234567890"}`},
		{"decimal", `{"decimal_value": "-0.001"}`},
		{"nested", `{"map_value": {"fields": {"a": {"list_value": {"fields": [{"bigint_value": "7"}, {"bytes_value": "0x01"}]}}}}}`},
		{"int64", `{"int64_value": "9007199254740993"}`},
		{"decimal scale", `{"decimal_value": "-1.00"}`},
		{"decimal positive exponent", `{"decimal_value": "12e3"}`},
		{"decimal tiny exponent", `{"decimal_value": "1e-200"}`},
		{"decimal zero", `{"decimal_value": "0.000"}`},
		{"bigint leading zero", `{"bigint_value": {"abs_val": "0x0001", "sign": "1"}}`},
		{"bigint signed zero", `{"bigint_value": {"abs_val": "0x", "sign": "1"}}`},
		{"bigint odd sign", `{"bigint_value": {"abs_val": "0x01", "sign": "5"}}`},
		{"decimal without coefficient", `{"decimal_value": {"coefficient": null, "exponent": 2}}`},
		{"decimal non-canonical coefficient", `{"decimal_value": {"coefficient": {"abs_val": "0x0001", "sign": "1"}, "exponent": 0}}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := &valuespb.Value{}
			require.NoError(t, fromPageJSON([]byte(tt.page), v))
			b, err := toPageJSON(v, true)
			require.NoError(t, err)
			assert.JSONEq(t, tt.page, string(b))
		})
	}
}

// wellKnown is a message with an Any and a BytesValue, whose JSON isn't their fields'.
func wellKnown(t *testing.T) *dynamicpb.Message {
	t.Helper()
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:       new("test.proto"),
		Package:    new("test"),
		Syntax:     new("proto3"),
		Dependency: []string{"google/protobuf/any.proto", "google/protobuf/wrappers.proto"},
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: new("WellKnown"),
			Field: []*descriptorpb.FieldDescriptorProto{
				{Name: new("any"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".google.protobuf.Any"), JsonName: new("any")},
				{Name: new("bytes"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_MESSAGE.Enum(), TypeName: new(".google.protobuf.BytesValue"), JsonName: new("bytes")},
			},
		}},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)
	return dynamicpb.NewMessage(fd.Messages().Get(0))
}

func TestJSONWellKnown(t *testing.T) {
	page := `{"any": {"@type": "type.googleapis.com/google.protobuf.Struct", "value": {"a": 1}}, "bytes": "0x01"}`
	m := wellKnown(t)
	require.NoError(t, fromPageJSON([]byte(page), m))
	b, err := toPageJSON(m, true)
	require.NoError(t, err)
	assert.JSONEq(t, page, string(b))
}

func TestJSONRoot(t *testing.T) {
	b := &wrapperspb.BytesValue{}
	require.NoError(t, fromPageJSON([]byte(`"0x01"`), b))
	assert.Equal(t, []byte{1}, b.Value)
	out, err := toPageJSON(b, true)
	require.NoError(t, err)
	assert.JSONEq(t, `"0x01"`, string(out))

	a := &anypb.Any{}
	page := `{"@type": "type.googleapis.com/google.protobuf.Struct", "value": {"a": 1}}`
	require.NoError(t, fromPageJSON([]byte(page), a))
	out, err = toPageJSON(a, true)
	require.NoError(t, err)
	assert.JSONEq(t, page, string(out))
}

func TestSchemaInput(t *testing.T) {
	s := formSchema{}
	assert.Equal(t, "timestamp", s.input((&timestamppb.Timestamp{}).ProtoReflect().Descriptor()).Kind)
	assert.Equal(t, "int", s.input((&wrapperspb.Int64Value{}).ProtoReflect().Descriptor()).Kind)
	assert.Equal(t, "message", s.input((&valuespb.BigInt{}).ProtoReflect().Descriptor()).Kind)
	assert.Equal(t, "message", s.input((&valuespb.Value{}).ProtoReflect().Descriptor()).Kind)
}

func TestJSONMalformedBytes(t *testing.T) {
	assert.Error(t, fromPageJSON([]byte(`{"bytes_value": {}}`), &valuespb.Value{}))
}

func TestJSONBase64LikeHex(t *testing.T) {
	// Their base64 is 0xAA, which would read back as hex.
	b, err := toPageJSON(&valuespb.Value{Value: &valuespb.Value_BytesValue{BytesValue: []byte{0xd3, 0x10, 0x00}}}, false)
	require.NoError(t, err)
	assert.JSONEq(t, `{"bytes_value": "0xd31000"}`, string(b))
}

func TestJSONBase64(t *testing.T) {
	v := &valuespb.Value{}
	require.NoError(t, fromPageJSON([]byte(`{"list_value": {"fields": [{"bytes_value": "AP8="}, {"bytes_value": "0x00ff"}]}}`), v))
	b, err := toPageJSON(v, false)
	require.NoError(t, err)
	assert.JSONEq(t, `{"list_value": {"fields": [{"bytes_value": "AP8="}, {"bytes_value": "AP8="}]}}`, string(b))
}

func TestJSONTemplate(t *testing.T) {
	b, err := toPageJSON(&valuespb.Decimal{}, false)
	require.NoError(t, err)
	assert.JSONEq(t, `{"coefficient": null, "exponent": 0}`, string(b))
}

func TestSchema(t *testing.T) {
	s := formSchema{}
	name := s.message((&valuespb.Value{}).ProtoReflect().Descriptor())
	fields := map[string]formField{}
	for _, f := range s[name] {
		fields[f.Name] = f
	}

	assert.Equal(t, formField{Name: "string_value", Type: "string", Kind: "string", Oneof: "value"}, fields["string_value"])
	assert.Equal(t, "bytes", fields["bytes_value"].Kind)
	assert.Equal(t, "bigint", fields["bigint_value"].Kind)
	assert.Equal(t, "decimal", fields["decimal_value"].Kind)
	assert.Equal(t, "timestamp", fields["time_value"].Kind)
	assert.Equal(t, "int", fields["int64_value"].Kind)

	mapFields := s[fields["map_value"].Message]
	require.Len(t, mapFields, 1)
	assert.Equal(t, "map", mapFields[0].Kind)
	assert.Equal(t, name, mapFields[0].Value.Message)
	listFields := s[fields["list_value"].Message]
	assert.True(t, listFields[0].List)
	assert.Equal(t, name, listFields[0].Message)
}

func TestSchemaEmptyMessage(t *testing.T) {
	s := formSchema{}
	assert.NotNil(t, s[s.message((&emptypb.Empty{}).ProtoReflect().Descriptor())])
}

func TestSubscriptionHistoryIsBounded(t *testing.T) {
	s := newDebugSubscriptions(nil)
	s.add(&debugSubscription{id: "id"})
	for range maxDebugEvents + 5 {
		s.publish("id", debugResult{})
	}

	sent := map[string]sentState{}
	views, _ := s.changes(sent)
	require.Len(t, views, 1)
	assert.Len(t, views[0].Events, maxDebugEvents)
	assert.Equal(t, maxDebugEvents+5, views[0].Total)

	s.publish("id", debugResult{})
	views, _ = s.changes(sent)
	require.Len(t, views, 1)
	assert.Len(t, views[0].Events, 1, "only what's new since")
}

func TestSchemaMetadata(t *testing.T) {
	s := formSchema{}
	fields := map[string]formField{}
	for _, f := range s[s.goStruct(reflect.TypeFor[capabilities.RequestMetadata]())] {
		fields[f.Name] = f
	}

	assert.Equal(t, "string", fields["WorkflowID"].Kind)
	assert.Equal(t, "number", fields["WorkflowDonID"].Kind)
	assert.Equal(t, "timestamp", fields["ExecutionTimestamp"].Kind)
	assert.True(t, fields["SpendLimits"].List)
	assert.Equal(t, "message", fields["SpendLimits"].Kind)
}

func TestGroupPayloads(t *testing.T) {
	h := &debugHandler{instances: make([]Capability, 3)}
	m := Method{Name: "Do", Input: &valuespb.Value{}}
	group := func(input string, instances ...int) debugGroup {
		return debugGroup{Instances: instances, Input: json.RawMessage(input)}
	}

	payloads, err := h.groupPayloads(m, []debugGroup{group(`{"string_value": "a"}`, 0, 2), group(`{"string_value": "b"}`, 1)})
	require.NoError(t, err)
	require.Len(t, payloads, 3)
	got := &valuespb.Value{}
	require.NoError(t, payloads[0].UnmarshalTo(got))
	assert.Equal(t, "a", got.GetStringValue())
	require.NoError(t, payloads[1].UnmarshalTo(got))
	assert.Equal(t, "b", got.GetStringValue())
	require.NoError(t, payloads[2].UnmarshalTo(got))
	assert.Equal(t, "a", got.GetStringValue(), "instances in a group get the same payload")

	payloads, err = h.groupPayloads(m, []debugGroup{group(`{}`, 0, 1)})
	require.NoError(t, err)
	assert.Nil(t, payloads[2], "an instance in no group is not called")

	for _, tt := range []struct {
		name   string
		groups []debugGroup
		want   string
	}{
		{"no instance in any group", []debugGroup{}, "no instance is in any group"},
		{"an instance in two groups", []debugGroup{group(`{}`, 0, 1), group(`{}`, 1, 2)}, "instance 1 is in two groups"},
		{"an instance there is not", []debugGroup{group(`{}`, 0, 1, 2, 3)}, "no instance 3"},
		{"a group claiming nothing", []debugGroup{group(`{}`), group(`{}`, 0, 1, 2)}, "claims no instances"},
		{"an input that is not the method's", []debugGroup{group(`{"nope": 1}`, 0, 1, 2)}, "group 1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.groupPayloads(m, tt.groups)
			require.ErrorContains(t, err, tt.want)
		})
	}
}

func TestFillDebugMetadata(t *testing.T) {
	var first, second capabilities.RequestMetadata
	fillDebugMetadata(&first)
	fillDebugMetadata(&second)
	assert.Len(t, first.WorkflowID, 64)
	assert.Len(t, first.WorkflowOwner, 40)
	assert.NotEqual(t, first.WorkflowID, second.WorkflowID, "every request gets its own workflow")
	assert.NotEqual(t, first.WorkflowExecutionID, second.WorkflowExecutionID)
	assert.False(t, first.ExecutionTimestamp.IsZero())

	given := capabilities.RequestMetadata{WorkflowID: "mine", WorkflowDonID: 7}
	fillDebugMetadata(&given)
	assert.Equal(t, "mine", given.WorkflowID, "what the page sends is kept")
	assert.Equal(t, uint32(7), given.WorkflowDonID)
	assert.NotEmpty(t, given.WorkflowOwner)
}
