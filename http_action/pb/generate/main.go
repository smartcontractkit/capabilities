package main

import (
	"github.com/smartcontractkit/capabilities/libs/protos"

	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"
)

func main() {
	gen := protos.ProtocGen{}
	if err := gen.Generate("github.com/smartcontractkit/capabilities/http_action/pb", &pkg.CapabilityConfig{
		Category:      "networking",
		Pkg:           "http",
		MajorVersion:  1,
		PreReleaseTag: "alpha",
		Files: []string{
			"client.proto",
		},
	}); err != nil {
		panic(err)
	}
}
