package main

import (
	"github.com/smartcontractkit/capabilities/libs/protos"
	"github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"
)

func main() {
	gen := protos.ProtocGen{}
	if err := gen.Generate(&pkg.CapabilityConfig{
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
