package main

import (
	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"

	"github.com/smartcontractkit/capabilities/libs/protos"
)

func main() {
	gen := protos.ProtocGen{}
	if err := gen.Generate("github.com/smartcontractkit/capabilities/cron/pb", &pkg.CapabilityConfig{
		Category:     "scheduler",
		Pkg:          "cron",
		MajorVersion: 1,
		Files: []string{
			"trigger.proto",
		},
	}); err != nil {
		panic(err)
	}
}
