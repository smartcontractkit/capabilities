package protos

import (
	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"
)

type ProtocHelper struct {
	fullPkg string
}

var _ pkg.ProtocHelper = ProtocHelper{}

func (g ProtocHelper) SdkPgk() string {
	return "github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/protoc/pb"
}

func (g ProtocHelper) FullGoPackageName(_ *pkg.CapabilityConfig) string {
	return g.fullPkg
}
