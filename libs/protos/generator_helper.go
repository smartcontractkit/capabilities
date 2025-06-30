package protos

import (
	"path/filepath"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/protoc/installer"
)

type GeneratorHelper struct{}

var _ installer.GeneratorHelper = GeneratorHelper{}

func (g GeneratorHelper) SdkPgk() string {
	return "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/protoc/pb"
}

func (g GeneratorHelper) PluginName() string {
	return "github.com/smartcontractkit/capabilities/bins/protoc-gen-capabilities"
}

func (g GeneratorHelper) HelperName() string {
	return "github.com/smartcontractkit/capabilities/libs"
}

func (g GeneratorHelper) FullGoPackageName(_ *installer.CapabilityConfig) string {
	absPath, _ := filepath.Abs(".")
	lastIndex := strings.LastIndex(absPath, "github.com")
	return absPath[lastIndex:]
}
