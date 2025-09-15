package main

import "github.com/smartcontractkit/chainlink-protos/cre/go/installer/pkg"

func main() {
	gen := &pkg.ProtocGen{
		Plugins: []pkg.Plugin{
			{Name: "go"},
			{Name: "go-grpc"},
		},
	}
	gen.AddSourceDirectories(".")
	gen.LinkPackage(pkg.Packages{
		Go:    "github.com/smartcontractkit/chainlink-protos/cre/values/v1",
		Proto: "values/v1/values.proto",
	})
	if err := gen.GenerateFile("ocr.proto", "."); err != nil {
		panic(err)
	}
}
