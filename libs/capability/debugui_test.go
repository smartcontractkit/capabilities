package capability

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities"
	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

// The debug UI is tested serving a capability with the generated example capability's tests. These test the parts
// of it that don't need one.

func TestJSON(t *testing.T) {
	for _, tt := range []struct{ name, page string }{
		{"bytes", `{"bytes_value": "0x00ff"}`},
		{"bigint", `{"bigint_value": "-123456789012345678901234567890"}`},
		{"decimal", `{"decimal_value": "-0.001"}`},
		{"nested", `{"map_value": {"fields": {"a": {"list_value": {"fields": [{"bigint_value": "7"}, {"bytes_value": "0x01"}]}}}}}`},
		{"int64", `{"int64_value": "9007199254740993"}`},
		{"decimal scale", `{"decimal_value": "-1.00"}`},
		{"decimal positive exponent", `{"decimal_value": "12e3"}`},
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
