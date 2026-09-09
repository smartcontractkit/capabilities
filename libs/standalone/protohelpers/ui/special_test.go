package ui

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	valuespb "github.com/smartcontractkit/chainlink-protos/cre/go/values/pb"
)

// The types are read off the generated code, so a rename upstream breaks the
// build here rather than quietly removing the widget.
func TestSpecialTypeNames(t *testing.T) {
	assert.Equal(t, "values.v1.BigInt", string(bigIntTypeName))
	assert.Equal(t, "values.v1.Decimal", string(decimalTypeName))
}

// A Value is a oneof holding either of them directly.
func TestSpecialPathsFindsBothInAValue(t *testing.T) {
	paths := specialPaths((&valuespb.Value{}).ProtoReflect().Descriptor())

	found := map[string]string{}
	for _, p := range paths {
		found[p.Path] = p.Type
	}

	assert.Equal(t, "values.v1.BigInt", found["bigint_value"])
	assert.Equal(t, "values.v1.Decimal", found["decimal_value"])
}

// A Decimal holds a BigInt coefficient, but that is part of the number beside it -
// offering a second box for it would be offering to edit half of one value.
func TestSpecialPathsDoesNotDescendIntoADecimal(t *testing.T) {
	paths := specialPaths((&valuespb.Value{}).ProtoReflect().Descriptor())

	for _, p := range paths {
		assert.NotContains(t, p.Path, "decimal_value.",
			"the coefficient of a decimal should not be offered separately")
	}
}

// A Decimal reached on its own still reports its coefficient, since there the
// coefficient is the whole message being looked at.
func TestSpecialPathsInsideADecimal(t *testing.T) {
	paths := specialPaths((&valuespb.Decimal{}).ProtoReflect().Descriptor())

	require.Len(t, paths, 1)
	assert.Equal(t, "coefficient", paths[0].Path)
	assert.Equal(t, "values.v1.BigInt", paths[0].Type)
}

// Repeated and map fields are marked, so the browser iterates rather than
// indexing straight in.
func TestSpecialPathsMarksListsAndMaps(t *testing.T) {
	// A Map's fields are map<string, Value>, and a List's are repeated Value, so
	// both reach a BigInt through a collection.
	mapPaths := specialPaths((&valuespb.Map{}).ProtoReflect().Descriptor())
	listPaths := specialPaths((&valuespb.List{}).ProtoReflect().Descriptor())

	var mapped, listed []string
	for _, p := range mapPaths {
		mapped = append(mapped, p.Path)
	}
	for _, p := range listPaths {
		listed = append(listed, p.Path)
	}

	assert.Contains(t, mapped, "fields{}.bigint_value")
	assert.Contains(t, listed, "fields[].bigint_value")
}

// A message that contains itself is walked once rather than forever: a Map holds
// Values and a Value can be a Map.
func TestSpecialPathsTerminatesOnRecursion(t *testing.T) {
	done := make(chan []SpecialPath, 1)
	go func() {
		done <- specialPaths((&valuespb.Map{}).ProtoReflect().Descriptor())
	}()

	select {
	case paths := <-done:
		assert.NotEmpty(t, paths)
	case <-time.After(5 * time.Second):
		t.Fatal("walking a self-containing message did not terminate")
	}
}

// A method with nothing special in its response is left out, so the page is not
// told to search a response that cannot contain one.
func TestSpecialConfigSkipsMethodsWithoutAny(t *testing.T) {
	server, _ := newTestServer(t)

	cfg := server.specialConfig()
	assert.Equal(t, "values.v1.BigInt", cfg.BigInt)
	assert.Equal(t, "values.v1.Decimal", cfg.Decimal)

	// Executable's unary methods answer Empty, so none of them can hold one.
	for method := range cfg.Methods {
		assert.NotContains(t, method, "RegisterToWorkflow", "%s should not be listed", method)
	}
}
