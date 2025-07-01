package protos

import (
	"path/filepath"
	"strings"

	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"
)

type ProtocHelper struct{}

var _ pkg.ProtocHelper = ProtocHelper{}

func (g ProtocHelper) SdkPgk() string {
	return "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/protoc/pb"
}

func (g ProtocHelper) FullGoPackageName(_ *pkg.CapabilityConfig) string {
	absPath, _ := filepath.Abs(".")
	lastIndex := strings.LastIndex(absPath, "github.com")
	return absPath[lastIndex:]
}
