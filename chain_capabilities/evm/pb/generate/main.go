package main

import (
	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"

	"github.com/smartcontractkit/capabilities/libs/protos"
)

func main() {
	gen := protos.ProtocGen{}
	if err := gen.Generate("github.com/smartcontractkit/capabilities/chain_capabilities/evm/pb", &pkg.CapabilityConfig{
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
