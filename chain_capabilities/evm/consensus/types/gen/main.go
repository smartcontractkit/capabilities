package main

import "github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"

func main() {
	gen := &pkg.ProtocGen{}
	gen.AddSourceDirectories(".")
	gen.LinkPackage(pkg.Packages{Go: "github.com/smartcontractkit/chainlink-common/pkg/values/pb", Proto: "values/v1/values.proto"})
	if err := gen.Generate("ocr.proto", "."); err != nil {
		panic(err)
	}
}
