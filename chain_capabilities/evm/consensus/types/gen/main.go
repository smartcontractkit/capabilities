package main

import "github.com/smartcontractkit/chainlink-protos/cre/go/installer/pkg"

func main() {
	gen := &pkg.ProtocGen{Plugins: []pkg.Plugin{{Name: "go-grpc"}, pkg.GoPlugin}}
	gen.AddSourceDirectories(".")
	if err := gen.GenerateFile("ocr.proto", "."); err != nil {
		panic(err)
	}
}
