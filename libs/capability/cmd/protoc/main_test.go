package main

import (
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/smartcontractkit/capabilities/libs/capability/feature"
)

func TestFeaturePkgMatchesProtosActualPackage(t *testing.T) {
	name := runtime.FuncForPC(reflect.ValueOf(feature.MarshalAny).Pointer()).Name()
	assert.Equal(t, featurePkg, strings.TrimSuffix(name, ".MarshalAny"))
}
