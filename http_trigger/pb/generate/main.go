package main

import (
	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"

	"github.com/smartcontractkit/capabilities/libs/protos"
)

func main() {
	gen := protos.ProtocGen{}
	if err := gen.Generate("github.com/smartcontractkit/capabilities/http_trigger/pb", &pkg.CapabilityConfig{
		Category:      "networking",
		Pkg:           "http",
		MajorVersion:  1,
		PreReleaseTag: "alpha",
		Files: []string{
			"trigger.proto",
		},
	}); err != nil {
		panic(err)
	}
}
