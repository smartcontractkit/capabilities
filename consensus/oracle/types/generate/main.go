package main

import "github.com/smartcontractkit/chainlink-protos/cre/go/installer/pkg"

func main() {
	gen := &pkg.ProtocGen{
		Plugins: []pkg.Plugin{
			{Name: "go"},
		},
	}
	gen.AddSourceDirectories(".")
	gen.LinkPackage(pkg.Packages{
		Go:    "github.com/smartcontractkit/chainlink-protos/cre/values/v1",
		Proto: "values/v1/values.proto",
	})
	gen.LinkPackage(pkg.Packages{
		Go:    "github.com/smartcontractkit/chainlink-protos/cre/sdk/v1alpha",
		Proto: "sdk/v1alpha/sdk.proto",
	})
	if err := gen.GenerateFile("value_consensus_types.proto", "."); err != nil {
		panic(err)
	}
}
