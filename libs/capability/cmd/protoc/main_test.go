package main

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/libs/capability/feature"
)

// TestFeaturePkg checks featurePkg is the package feature.proto's Go is in.
func TestFeaturePkg(t *testing.T) {
	name := runtime.FuncForPC(reflect.ValueOf(feature.MarshalAny).Pointer()).Name()
	assert.Equal(t, featurePkg, strings.TrimSuffix(name, ".MarshalAny"))
}
