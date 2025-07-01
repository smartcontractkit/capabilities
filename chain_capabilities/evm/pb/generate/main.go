package main

import (
	"github.com/smartcontractkit/capabilities/libs/protos"
	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"
)

func main() {
	gen := protos.ProtocGen{}
	if err := gen.Generate(&pkg.CapabilityConfig{
		Category:      "blockchain",
		Pkg:           "evm",
		MajorVersion:  1,
		PreReleaseTag: "alpha",
		Files: []string{
			"client.proto",
		},
	}); err != nil {
		panic(err)
	}
}
